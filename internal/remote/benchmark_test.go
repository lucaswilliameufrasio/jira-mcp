//go:build benchmark

// Remote-mode benchmark harness. It measures latency percentiles, throughput,
// error rate and resource usage of the remote MCP server under three setups:
//
//   - inproc: server wired in-process (fast iteration, pprof; CPU includes
//     the benchmark workers themselves, so treat CPU numbers as an upper bound)
//   - binary: production binary started as a subprocess (pure server resources)
//   - docker: the Railway image in a container on a dedicated network (what
//     production will actually run)
//
// Scenarios: ping, tools_list and a real jira_search tool call against a fake
// Jira API, each at concurrency 1/10/50/100 with warmup and N runs.
package remote

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const benchToken = "bench-mcp-token"

var benchToolNames = []string{
	"jira_search", "jira_get_issue", "jira_create_issue", "jira_update_issue",
	"jira_add_comment", "jira_list_transitions", "jira_transition_issue",
	"jira_list_projects", "jira_assign_issue", "jira_list_boards",
	"jira_get_board", "jira_list_sprints", "jira_board_issues",
	"jira_sprint_issues", "jira_list_attachments",
}

func benchEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func benchEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func benchMS(d time.Duration) string {
	return fmt.Sprintf("%.2fms", float64(d)/1e6)
}

func benchPercentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

type benchFakeJira struct {
	server *httptest.Server
	hits   int64
}

func newBenchFakeJira(allInterfaces bool) (*benchFakeJira, error) {
	f := &benchFakeJira{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&f.hits, 1)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/rest/api/3/search/jql") && r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issues": []map[string]any{{"id": "10001", "key": "BENCH-1", "fields": map[string]any{"summary": "bench issue"}}},
				"isLast": true,
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/rest/api/3/myself") {
			_ = json.NewEncoder(w).Encode(map[string]any{"accountId": "bench-account", "displayName": "Bench"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})
	if allInterfaces {
		listener, err := net.Listen("tcp", ":0")
		if err != nil {
			return nil, err
		}
		f.server = httptest.NewUnstartedServer(handler)
		f.server.Listener = listener
		f.server.Start()
	} else {
		f.server = httptest.NewServer(handler)
	}
	return f, nil
}

func benchFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, err
	}
	return port, nil
}

func benchConnect(ctx context.Context, endpoint, token string) (*sdk.ClientSession, error) {
	client := sdk.NewClient(&sdk.Implementation{Name: "bench-client", Version: "1.0.0"}, nil)
	transport := &sdk.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}},
		DisableStandaloneSSE: true,
	}
	return client.Connect(ctx, transport, nil)
}

type benchOp func(ctx context.Context, s *sdk.ClientSession) error

func benchScenarioOp(name, endpoint, token string) benchOp {
	switch name {
	case "ping":
		return func(ctx context.Context, s *sdk.ClientSession) error { return s.Ping(ctx, nil) }
	case "tools_list":
		return func(ctx context.Context, s *sdk.ClientSession) error {
			_, err := s.ListTools(ctx, nil)
			return err
		}
	case "search":
		return func(ctx context.Context, s *sdk.ClientSession) error {
			_, err := s.CallTool(ctx, &sdk.CallToolParams{Name: "jira_search", Arguments: map[string]any{"jql": "project = BENCH"}})
			return err
		}
	case "tools_list_raw":
		return benchRawOp(endpoint, token, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	}
	return nil
}

// benchRawOp sends a pre-marshaled JSON-RPC request over plain HTTP and
// discards the response body. It measures the wire path without SDK client
// parsing, isolating client-side JSON cost from server capacity.
func benchRawOp(endpoint, token string, body []byte) benchOp {
	client := &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}, Timeout: 15 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	initReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"bench-raw","version":"1"}}}`))
	if err != nil {
		return func(context.Context, *sdk.ClientSession) error { return err }
	}
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Accept", "application/json, text/event-stream")
	res, err := client.Do(initReq)
	if err != nil {
		return func(context.Context, *sdk.ClientSession) error { return err }
	}
	sessionID := res.Header.Get("Mcp-Session-Id")
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return func(ctx context.Context, _ *sdk.ClientSession) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		_, err = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		return err
	}
}

func readBenchProcStat(pid int) (ticks float64, rssMB float64, err error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, err
	}
	s := string(raw)
	if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 <= len(s) {
		s = s[i+2:]
	}
	fields := strings.Fields(s)
	if len(fields) < 13 {
		return 0, 0, fmt.Errorf("unexpected /proc/%d/stat layout", pid)
	}
	ut, _ := strconv.ParseFloat(fields[11], 64)
	st, _ := strconv.ParseFloat(fields[12], 64)
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			kb, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, "VmRSS:")), "kB")), 64)
			return ut + st, kb / 1024, nil
		}
	}
	return 0, 0, fmt.Errorf("VmRSS not found for pid %d", pid)
}

func benchProcSource(pid int) func() (float64, float64) {
	var prevTicks float64
	var prevAt time.Time
	return func() (float64, float64) {
		ticks, rss, err := readBenchProcStat(pid)
		if err != nil {
			return -1, -1
		}
		defer func() { prevTicks, prevAt = ticks, time.Now() }()
		if prevAt.IsZero() {
			return -1, rss
		}
		elapsed := time.Since(prevAt).Seconds()
		if elapsed <= 0 {
			return -1, rss
		}
		// CLK_TCK is 100 on Linux.
		return ((ticks - prevTicks) / 100.0) / elapsed * 100, rss
	}
}

func benchParseMem(token string) float64 {
	for _, unit := range []struct {
		suffix     string
		multiplier float64
	}{{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}} {
		if strings.HasSuffix(token, unit.suffix) {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(token, unit.suffix), 64)
			return v * unit.multiplier
		}
	}
	return -1
}

func benchDockerSource(name string) func() (float64, float64) {
	cmd := exec.Command("docker", "stats", "--format", "{{.CPUPerc}} {{.MemUsage}}", name)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return func() (float64, float64) { return -1, -1 }
	}
	if err := cmd.Start(); err != nil {
		return func() (float64, float64) { return -1, -1 }
	}
	var mu sync.Mutex
	var lastCPU, lastRSS float64
	var hasValue bool
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}
			cpu, _ := strconv.ParseFloat(strings.TrimSuffix(fields[0], "%"), 64)
			rss := benchParseMem(fields[1])
			mu.Lock()
			lastCPU, lastRSS, hasValue = cpu, rss, true
			mu.Unlock()
		}
	}()
	return func() (float64, float64) {
		mu.Lock()
		defer mu.Unlock()
		if !hasValue {
			return -1, -1
		}
		return lastCPU, lastRSS
	}
}

type benchSampler struct {
	mu      sync.Mutex
	stop    chan struct{}
	stopped chan struct{}
	cpu     []float64
	rss     []float64
}

func startBenchSampler(source func() (float64, float64), interval time.Duration) *benchSampler {
	s := &benchSampler{stop: make(chan struct{}), stopped: make(chan struct{})}
	go func() {
		defer close(s.stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				cpu, rss := source()
				s.mu.Lock()
				if rss >= 0 {
					s.rss = append(s.rss, rss)
				}
				if cpu >= 0 {
					s.cpu = append(s.cpu, cpu)
				}
				s.mu.Unlock()
			}
		}
	}()
	return s
}

func (s *benchSampler) begin() {
	s.mu.Lock()
	s.cpu = s.cpu[:0]
	s.rss = s.rss[:0]
	s.mu.Unlock()
}

func (s *benchSampler) sampleNow(source func() (float64, float64)) {
	cpu, rss := source()
	s.mu.Lock()
	if rss >= 0 {
		s.rss = append(s.rss, rss)
	}
	if cpu >= 0 {
		s.cpu = append(s.cpu, cpu)
	}
	s.mu.Unlock()
}

func (s *benchSampler) stopSampler() {
	close(s.stop)
	<-s.stopped
}

func (s *benchSampler) summary() (cpuAvg, cpuPeak, rssAvg, rssPeak float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.cpu {
		cpuAvg += v
		if v > cpuPeak {
			cpuPeak = v
		}
	}
	if len(s.cpu) > 0 {
		cpuAvg /= float64(len(s.cpu))
	}
	for _, v := range s.rss {
		rssAvg += v
		if v > rssPeak {
			rssPeak = v
		}
	}
	if len(s.rss) > 0 {
		rssAvg /= float64(len(s.rss))
	}
	return
}

func benchStatsLine(mode, scenario string, conc, run, runs, ops int, lat []time.Duration, elapsed time.Duration, errs int64, firstErr string, s, client *benchSampler) {
	sorted := append([]time.Duration(nil), lat...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var total time.Duration
	for _, d := range sorted {
		total += d
	}
	avg := time.Duration(0)
	if len(sorted) > 0 {
		avg = total / time.Duration(len(sorted))
	}
	rps := float64(ops) / elapsed.Seconds()
	cpuAvg, cpuPeak, rssAvg, rssPeak := s.summary()
	clientPart := ""
	if client != nil {
		cc, _, _, _ := client.summary()
		clientPart = fmt.Sprintf(" cpu_client=%.0f%%", cc)
	}
	errPart := ""
	if firstErr != "" {
		if len(firstErr) > 90 {
			firstErr = firstErr[:90]
		}
		errPart = fmt.Sprintf(" err_sample=%q", firstErr)
	}
	fmt.Printf("BENCH mode=%s scenario=%s conc=%d run=%d/%d ops=%d p50=%s p95=%s p99=%s avg=%s max=%s rps=%.0f errs=%d cpu_avg=%.0f%% cpu_peak=%.0f%%%s%s rss_avg=%.1fMB rss_peak=%.1fMB\n",
		mode, scenario, conc, run, runs, ops,
		benchMS(benchPercentile(sorted, 0.50)), benchMS(benchPercentile(sorted, 0.95)), benchMS(benchPercentile(sorted, 0.99)),
		benchMS(avg), benchMS(sorted[len(sorted)-1]), rps, errs, cpuAvg, cpuPeak, clientPart, errPart, rssAvg, rssPeak)
}

type benchServer interface {
	Endpoint() string
	Source() func() (float64, float64)
	ColdStart(ctx context.Context, runs int) error
}

type benchInprocServer struct {
	endpoint string
	source   func() (float64, float64)
	heapPath string
}

func (b *benchInprocServer) Endpoint() string                         { return b.endpoint }
func (b *benchInprocServer) Source() func() (float64, float64)        { return b.source }
func (b *benchInprocServer) ColdStart(_ context.Context, _ int) error { return nil }

type benchBinaryServer struct {
	binaryPath string
	cmd        *exec.Cmd
	port       int
	env        []string
}

func startBenchBinary(binaryPath string, port int, env []string) (*benchBinaryServer, error) {
	b := &benchBinaryServer{binaryPath: binaryPath, port: port, env: env}
	if err := b.start(); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *benchBinaryServer) start() error {
	cmd := exec.Command(b.binaryPath, "remote")
	cmd.Env = b.env
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	b.cmd = cmd
	if err := benchWaitHealth(fmt.Sprintf("http://127.0.0.1:%d/health", b.port), 30*time.Second); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return err
	}
	return nil
}

func (b *benchBinaryServer) stop() {
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
		_, _ = b.cmd.Process.Wait()
		b.cmd = nil
	}
}

func (b *benchBinaryServer) Endpoint() string { return fmt.Sprintf("http://127.0.0.1:%d/mcp", b.port) }
func (b *benchBinaryServer) Source() func() (float64, float64) {
	pid := b.cmd.Process.Pid
	return benchProcSource(pid)
}
func (b *benchBinaryServer) ColdStart(ctx context.Context, runs int) error {
	for i := 1; i <= runs; i++ {
		b.stop()
		started := time.Now()
		if err := b.start(); err != nil {
			return err
		}
		health := time.Since(started)
		pingStart := time.Now()
		s, err := benchConnect(ctx, b.Endpoint(), benchToken)
		if err != nil {
			return err
		}
		if err := s.Ping(ctx, nil); err != nil {
			return err
		}
		_ = s.Close()
		fmt.Printf("BENCH mode=binary coldstart run=%d health=%s first_ping=%s\n", i, benchMS(health), benchMS(time.Since(pingStart)))
	}
	return nil
}

func benchWaitHealth(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		res, err := client.Get(url)
		if err == nil {
			code := res.StatusCode
			_ = res.Body.Close()
			if code == http.StatusOK {
				return nil
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("health check on %s timed out", url)
}

func benchPingValkey(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		return err
	}
	buf := make([]byte, 7)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if !strings.HasPrefix(string(buf), "+PONG") {
		return fmt.Errorf("unexpected valkey reply %q", strings.TrimSpace(string(buf)))
	}
	return nil
}

func benchDockerExec(args ...string) (string, error) {
	out, err := exec.Command("docker", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

type benchDockerServer struct {
	network      string
	container    string
	valkeyName   string
	fakeJiraName string
	image        string
	port         int
	valkeyHost   string
	containerEnv []string
	endpoint     string
	fakeConf     string
}

const benchFakeNginxConf = `server {
    listen 80 default_server;
    default_type application/json;
    location = /ex/jira/bench-cloud/rest/api/3/search/jql {
        return 200 '{"issues":[{"id":"10001","key":"BENCH-1","fields":{"summary":"bench issue"}}],"isLast":true}';
    }
    location = /ex/jira/bench-cloud/rest/api/3/myself {
        return 200 '{"accountId":"bench-account","displayName":"Bench"}';
    }
    location / {
        return 200 '{}';
    }
}
`

func setupBenchDocker(ctx context.Context, image string, key []byte) (*benchDockerServer, string, error) {
	d := &benchDockerServer{
		network:      "jira-mcp-bench-net",
		container:    "jira-mcp-bench",
		valkeyName:   "jira-mcp-bench-valkey",
		fakeJiraName: "jira-mcp-bench-fake-jira",
		image:        image,
	}
	_, _ = benchDockerExec("rm", "-f", d.container, d.valkeyName, d.fakeJiraName)
	_, _ = benchDockerExec("network", "rm", d.network)
	if out, err := benchDockerExec("network", "create", d.network); err != nil {
		return nil, "", fmt.Errorf("network create: %s: %w", out, err)
	}
	conf, err := os.CreateTemp("", "jira-mcp-bench-fake-jira-*.conf")
	if err != nil {
		d.teardown()
		return nil, "", err
	}
	if _, err := conf.WriteString(benchFakeNginxConf); err != nil {
		_ = conf.Close()
		d.teardown()
		return nil, "", err
	}
	if err := conf.Close(); err != nil {
		d.teardown()
		return nil, "", err
	}
	d.fakeConf = conf.Name()
	cleanupFakeConf := func() { _ = os.Remove(d.fakeConf) }
	valkeyPort, err := benchFreePort()
	if err != nil {
		cleanupFakeConf()
		d.teardown()
		return nil, "", err
	}
	if out, err := benchDockerExec("run", "-d", "--name", d.valkeyName, "--network", d.network,
		"-p", fmt.Sprintf("127.0.0.1:%d:6379", valkeyPort), "valkey/valkey:9.1"); err != nil {
		cleanupFakeConf()
		d.teardown()
		return nil, "", fmt.Errorf("valkey run: %s: %w", out, err)
	}
	if out, err := benchDockerExec("run", "-d", "--name", d.fakeJiraName, "--network", d.network,
		"--network-alias", "fake-jira", "-v", d.fakeConf+":/etc/nginx/conf.d/default.conf:ro", "nginx:alpine"); err != nil {
		cleanupFakeConf()
		d.teardown()
		return nil, "", fmt.Errorf("fake jira run: %s: %w", out, err)
	}
	d.valkeyHost = fmt.Sprintf("redis://127.0.0.1:%d", valkeyPort)
	waitDeadline := time.Now().Add(30 * time.Second)
	for {
		err := benchPingValkey(fmt.Sprintf("127.0.0.1:%d", valkeyPort))
		if err == nil {
			break
		}
		if time.Now().After(waitDeadline) {
			cleanupFakeConf()
			d.teardown()
			return nil, "", fmt.Errorf("valkey did not answer PING: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	fakeDeadline := time.Now().Add(30 * time.Second)
	for {
		out, err := benchDockerExec("exec", d.fakeJiraName, "wget", "-qO-", "http://127.0.0.1/ex/jira/bench-cloud/rest/api/3/search/jql")
		if err == nil && strings.Contains(out, "BENCH-1") {
			break
		}
		if time.Now().After(fakeDeadline) {
			cleanupFakeConf()
			d.teardown()
			return nil, "", fmt.Errorf("fake jira did not come up: %s: %w", out, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	port, err := benchFreePort()
	if err != nil {
		cleanupFakeConf()
		d.teardown()
		return nil, "", err
	}
	d.port = port
	d.endpoint = fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	d.containerEnv = []string{
		"PORT=18082",
		"HOST=0.0.0.0",
		"VALKEY_URL=redis://" + d.valkeyName + ":6379",
		fmt.Sprintf("JIRA_MCP_PUBLIC_URL=http://127.0.0.1:%d", port),
		"JIRA_MCP_ATLASSIAN_CLIENT_ID=bench",
		"JIRA_MCP_ATLASSIAN_CLIENT_SECRET=bench",
		"JIRA_MCP_ENCRYPTION_KEY=" + base64.RawStdEncoding.EncodeToString(key),
		"JIRA_MCP_ATLASSIAN_API_URL=http://fake-jira",
		"JIRA_MCP_RATE_LIMIT_RPS=0",
	}
	return d, d.valkeyHost, nil
}

func (d *benchDockerServer) run() error {
	args := []string{"run", "-d", "--name", d.container, "--network", d.network,
		"-p", fmt.Sprintf("127.0.0.1:%d:18082", d.port)}
	for _, e := range d.containerEnv {
		args = append(args, "-e", e)
	}
	args = append(args, d.image)
	if out, err := benchDockerExec(args...); err != nil {
		return fmt.Errorf("bench container run: %s: %w", out, err)
	}
	return benchWaitHealth(fmt.Sprintf("http://127.0.0.1:%d/health", d.port), 60*time.Second)
}

func (d *benchDockerServer) teardown() {
	_, _ = benchDockerExec("rm", "-f", d.container, d.valkeyName, d.fakeJiraName)
	_, _ = benchDockerExec("network", "rm", d.network)
	if d.fakeConf != "" {
		_ = os.Remove(d.fakeConf)
		d.fakeConf = ""
	}
}

func (d *benchDockerServer) Endpoint() string { return d.endpoint }
func (d *benchDockerServer) Source() func() (float64, float64) {
	if out, err := benchDockerExec("inspect", "-f", "{{.State.Pid}}", d.container); err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(out)); perr == nil && pid > 0 {
			return benchProcSource(pid)
		}
	}
	return benchDockerSource(d.container)
}
func (d *benchDockerServer) ColdStart(ctx context.Context, runs int) error {
	for i := 1; i <= runs; i++ {
		_, _ = benchDockerExec("rm", "-f", d.container)
		started := time.Now()
		if err := d.run(); err != nil {
			return err
		}
		health := time.Since(started)
		pingStart := time.Now()
		s, err := benchConnect(ctx, d.Endpoint(), benchToken)
		if err != nil {
			return err
		}
		if err := s.Ping(ctx, nil); err != nil {
			return err
		}
		_ = s.Close()
		fmt.Printf("BENCH mode=docker coldstart run=%d health=%s first_ping=%s\n", i, benchMS(health), benchMS(time.Since(pingStart)))
	}
	return nil
}

func TestRemoteBenchmark(t *testing.T) {
	if os.Getenv("JIRA_MCP_BENCH") == "" {
		t.Skip("JIRA_MCP_BENCH is required to run the remote benchmark")
	}
	mode := benchEnv("JIRA_MCP_BENCH_MODE", "inproc")
	runs := benchEnvInt("JIRA_MCP_BENCH_RUNS", 5)
	coldRuns := benchEnvInt("JIRA_MCP_BENCH_COLD", 3)
	totalOps := benchEnvInt("JIRA_MCP_BENCH_OPS", 800)

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	var cleanup []func()
	defer func() {
		for i := len(cleanup) - 1; i >= 0; i-- {
			cleanup[i]()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	var storeURL string
	var server benchServer
	switch mode {
	case "inproc", "binary":
		storeURL = benchEnv("VALKEY_URL", "redis://127.0.0.1:6379")
	case "docker":
		image := benchEnv("JIRA_MCP_BENCH_IMAGE", "jira-mcp:bench")
		var valkeyHostURL string
		var err error
		server, valkeyHostURL, err = setupBenchDocker(ctx, image, key)
		if err != nil {
			t.Fatal(err)
		}
		cleanup = append(cleanup, server.(*benchDockerServer).teardown)
		storeURL = valkeyHostURL
	default:
		t.Fatalf("unknown JIRA_MCP_BENCH_MODE %q", mode)
	}

	store, err := NewValkeyStore(storeURL)
	if err != nil {
		t.Fatal(err)
	}
	cleanup = append(cleanup, store.Close)

	accessToken, err := seal(key, "bench-access-token")
	if err != nil {
		t.Fatal(err)
	}
	seedCtx, seedCancel := context.WithTimeout(ctx, 15*time.Second)
	defer seedCancel()
	if err := store.Put(seedCtx, "mcp:user:bench-user", userRecord{
		AccountID: "bench-account", CloudID: "bench-cloud", Tools: benchToolNames,
		AccessToken: accessToken, ExpiresAt: time.Now().Add(24 * time.Hour),
	}, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(seedCtx, "mcp:token:"+benchToken, accessSession{UserID: "bench-user"}, 24*time.Hour); err != nil {
		t.Fatal(err)
	}

	fake, err := newBenchFakeJira(mode == "docker")
	if err != nil {
		t.Fatal(err)
	}
	cleanup = append(cleanup, fake.server.Close)

	switch mode {
	case "inproc":
		srv, err := New(Config{
			PublicURL: "http://127.0.0.1", AtlassianID: "bench", AtlassianKey: "bench",
			AtlassianAPIURL: fake.server.URL, Store: store, EncryptionKey: key,
			RateLimitRPS: 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(srv)
		cleanup = append(cleanup, ts.Close)
		server = &benchInprocServer{endpoint: ts.URL + "/mcp", source: benchProcSource(os.Getpid()), heapPath: "/tmp/jira-mcp-bench-heap.pprof"}
	case "binary":
		binaryPath := os.Getenv("JIRA_MCP_BENCH_BINARY")
		if binaryPath == "" {
			t.Fatal("JIRA_MCP_BENCH_BINARY is required in binary mode")
		}
		port, err := benchFreePort()
		if err != nil {
			t.Fatal(err)
		}
		env := append(os.Environ(),
			"VALKEY_URL="+storeURL,
			fmt.Sprintf("HOST=127.0.0.1"), fmt.Sprintf("PORT=%d", port),
			fmt.Sprintf("JIRA_MCP_PUBLIC_URL=http://127.0.0.1:%d", port),
			"JIRA_MCP_ATLASSIAN_CLIENT_ID=bench",
			"JIRA_MCP_ATLASSIAN_CLIENT_SECRET=bench",
			"JIRA_MCP_ENCRYPTION_KEY="+base64.RawStdEncoding.EncodeToString(key),
			"JIRA_MCP_ATLASSIAN_API_URL="+fake.server.URL,
			"JIRA_MCP_RATE_LIMIT_RPS=0",
		)
		bs, err := startBenchBinary(binaryPath, port, env)
		if err != nil {
			t.Fatal(err)
		}
		cleanup = append(cleanup, bs.stop)
		server = bs
	case "docker":
		ds := server.(*benchDockerServer)
		if err := ds.run(); err != nil {
			t.Fatal(err)
		}
	}

	if err := server.ColdStart(ctx, coldRuns); err != nil {
		t.Fatal(err)
	}

	sampler := startBenchSampler(server.Source(), 100*time.Millisecond)
	cleanup = append(cleanup, sampler.stopSampler)

	var clientSampler *benchSampler
	if mode == "binary" || mode == "docker" {
		clientSampler = startBenchSampler(benchProcSource(os.Getpid()), 100*time.Millisecond)
		cleanup = append(cleanup, clientSampler.stopSampler)
	}

	fmt.Printf("BENCH host_cores=%d mode=%s runs=%d ops_per_run=%d\n", runtime.NumCPU(), mode, runs, totalOps)

	var selected []string
	if filter := os.Getenv("JIRA_MCP_BENCH_SCENARIOS"); filter != "" {
		for _, name := range strings.Split(filter, ",") {
			selected = append(selected, strings.TrimSpace(name))
		}
	} else {
		selected = []string{"ping", "tools_list", "tools_list_raw", "search"}
	}

	profile := benchEnv("JIRA_MCP_BENCH_PPROF", "") == "1"
	var cpuFile *os.File
	var profiling bool

	concs := []int{1, 10, 50, 100}
	for _, scenario := range selected {
		op := benchScenarioOp(scenario, server.Endpoint(), benchToken)
		for _, conc := range concs {
			sessions := make([]*sdk.ClientSession, conc)
			setupStart := time.Now()
			for i := range sessions {
				s, err := benchConnect(ctx, server.Endpoint(), benchToken)
				if err != nil {
					t.Fatalf("session %d: %v", i, err)
				}
				sessions[i] = s
			}
			fmt.Printf("BENCH mode=%s scenario=%s conc=%d sessions=%d setup=%s\n", mode, scenario, conc, conc, benchMS(time.Since(setupStart)))
			for _, s := range sessions {
				for i := 0; i < 20; i++ {
					if err := op(ctx, s); err != nil {
						t.Fatalf("warmup: %v", err)
					}
				}
			}
			opsPerWorker := totalOps / conc
			for run := 1; run <= runs; run++ {
				if profile && scenario == "tools_list" && conc == 100 && run == 1 {
					f, err := os.Create("/tmp/jira-mcp-bench-cpu.pprof")
					if err == nil {
						cpuFile = f
						if pprof.StartCPUProfile(f) == nil {
							profiling = true
						}
					}
				}
				sampler.begin()
				runCtx, runCancel := context.WithTimeout(ctx, 2*time.Minute)
				lat := make([]time.Duration, 0, conc*opsPerWorker)
				var mu sync.Mutex
				var errs int64
				var firstErr string
				started := time.Now()
				var wg sync.WaitGroup
				for _, s := range sessions {
					wg.Add(1)
					go func(s *sdk.ClientSession) {
						defer wg.Done()
						local := make([]time.Duration, 0, opsPerWorker)
						var localErrs int64
						for i := 0; i < opsPerWorker; i++ {
							t0 := time.Now()
							if err := op(runCtx, s); err != nil {
								localErrs++
								mu.Lock()
								if firstErr == "" {
									firstErr = err.Error()
								}
								mu.Unlock()
							}
							local = append(local, time.Since(t0))
						}
						mu.Lock()
						lat = append(lat, local...)
						errs += localErrs
						mu.Unlock()
					}(s)
				}
				wg.Wait()
				elapsed := time.Since(started)
				runCancel()
				sampler.sampleNow(server.Source())
				if clientSampler != nil {
					clientSampler.sampleNow(benchProcSource(os.Getpid()))
				}
				if profile && scenario == "tools_list" && conc == 100 && run == runs && profiling {
					pprof.StopCPUProfile()
					profiling = false
					_ = cpuFile.Close()
					fmt.Printf("BENCH pprof cpu profile written to /tmp/jira-mcp-bench-cpu.pprof (symbolize with: go tool pprof -top %s /tmp/jira-mcp-bench-cpu.pprof)\n", os.Args[0])
				}
				benchStatsLine(mode, scenario, conc, run, runs, conc*opsPerWorker, lat, elapsed, errs, firstErr, sampler, clientSampler)
			}
			for _, s := range sessions {
				_ = s.Close()
			}
		}
	}

	if mode == "inproc" {
		if f, err := os.Create("/tmp/jira-mcp-bench-heap.pprof"); err == nil {
			if pprof.Lookup("heap").WriteTo(f, 0) == nil {
				fmt.Printf("BENCH pprof heap profile written to /tmp/jira-mcp-bench-heap.pprof\n")
			}
			_ = f.Close()
		}
	}
	if mode != "docker" {
		fmt.Printf("BENCH fake_jira_hits=%d\n", atomic.LoadInt64(&fake.hits))
	}
}
