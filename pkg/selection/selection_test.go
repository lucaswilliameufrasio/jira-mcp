package selection

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "mixed names and indexes", input: "2,one,2", want: "two,one"},
		{name: "all", input: "all", want: "one,two,three"},
		{name: "empty selects all", input: "", want: "one,two,three"},
		{name: "invalid index", input: "0", wantErr: true},
		{name: "invalid name", input: "four", wantErr: true},
	}
	choices := []string{"one", "two", "three"}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.input, choices)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || strings.Join(got, ",") != test.want {
				t.Fatalf("got %v, err %v", got, err)
			}
		})
	}
}
