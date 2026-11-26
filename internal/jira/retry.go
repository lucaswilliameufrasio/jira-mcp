package jira

import (
	"net/http"
	"strconv"
	"time"
)

// Retry at most once and only when the caller explicitly identifies a read.
func readWithRetry(client *http.Client, req *http.Request, safe bool) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil || !safe || resp.StatusCode != http.StatusTooManyRequests {
		return resp, err
	}
	value := resp.Header.Get("Retry-After")
	var delay time.Duration
	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		delay = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(value); err == nil {
		delay = time.Until(at)
	} else {
		return resp, nil
	}
	if delay > 5*time.Second {
		return resp, nil
	}
	if deadline, ok := req.Context().Deadline(); ok && delay >= time.Until(deadline) {
		return resp, nil
	}
	_ = resp.Body.Close()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-timer.C:
	}
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		retry.Body, err = req.GetBody()
		if err != nil {
			return nil, err
		}
	}
	return client.Do(retry)
}
