package main

import (
	"fmt"
	"log"

	"github.com/davecgh/go-spew/spew"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/rpc2"
	"github.com/knusbaum/acmetools"
)

func main() {

	a, err := acmetools.NewAcme()
	if err != nil {
		log.Fatalf("Failed to connect to acme: %v", err)
	}
	//spew.Dump(a)
	w, err := a.NewWindow()
	if err != nil {
		log.Fatalf("Failed to create dlv window: %v", err)
	}
	//spew.Dump(w)
	fmt.Printf("Window: %v\n", w)
	//go9p.Verbose = true
	es, err := w.Events()
	if err != nil {
		log.Fatalf("Failed to get events stream: %v", err)
	}
	defer es.Close()
	for {
		fmt.Printf("Event: %+v\n", <-es.C)
	}
}

func testRPC() {
	c := rpc2.NewClient("localhost:8181")
	// 	if err != nil {
	// 		log.Fatalf("Failed to dial client: %v", err)
	// 	}
	bp, err := c.CreateBreakpoint(&api.Breakpoint{
		File: "/home/kjn/CodeBase/datadog-agent/pkg/trace/agent/agent_test.go",
		Line: 63,
	})
	if err != nil {
		log.Fatalf("Failed to create breakpoint: %v", err)
	}
	//spew.Dump(bp)
	log.Printf("Created breakpoint in %s", bp.FunctionName)

	state := <-c.Continue()
	spew.Dump(state)
}
