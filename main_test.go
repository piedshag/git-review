package main

import (
	"flag"
	"io"
	"reflect"
	"testing"
)

func TestParseInterspersedFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "flags first", args: []string{"-v", "--base", "main", "feature"}},
		{name: "branch first", args: []string{"feature", "-v", "--base", "main"}},
		{name: "flags around branch", args: []string{"--base=main", "feature", "-v"}},
		{name: "explicit boolean", args: []string{"feature", "-v=false", "--base", "main"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			verbose := fs.Bool("v", false, "verbose")
			base := fs.String("base", "", "base")
			if err := parseInterspersed(fs, test.args); err != nil {
				t.Fatal(err)
			}
			expectedVerbose := test.name != "explicit boolean"
			if *verbose != expectedVerbose || *base != "main" || !reflect.DeepEqual(fs.Args(), []string{"feature"}) {
				t.Fatalf("verbose=%t base=%q args=%v", *verbose, *base, fs.Args())
			}
		})
	}
}

func TestParseInterspersedHonorsDoubleDash(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	verbose := fs.Bool("v", false, "verbose")
	if err := parseInterspersed(fs, []string{"-v", "--", "-branch"}); err != nil {
		t.Fatal(err)
	}
	if !*verbose || !reflect.DeepEqual(fs.Args(), []string{"-branch"}) {
		t.Fatalf("verbose=%t args=%v", *verbose, fs.Args())
	}
}
