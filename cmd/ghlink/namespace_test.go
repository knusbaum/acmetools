package main

import "testing"

func TestCanonicalize(t *testing.T) {
	for _, tt := range []struct {
		in  string
		out string
	}{
		{
			in:  ":0.0",
			out: ":0",
		},
		{
			in:  ":01234",
			out: ":01234",
		},
		{
			in:  "foobar:01234.0",
			out: "foobar:01234",
		},
		{
			in:  "0.1.0.1.0",
			out: "0.1.0.1.0",
		},
	} {
		t.Run(tt.in, func(t *testing.T) {
			if out := canonicalize(tt.in); out != tt.out {
				t.Fatalf("Expected %s, but got %s", tt.out, out)
			}
		})
	}
}
