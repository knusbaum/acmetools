package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"github.com/knusbaum/acmetools"
)

var plumb = flag.Bool("p", false, "Causes ghlink to plumb the link rather than printing it.")

func main() {
	flag.Parse()

	winid := os.Getenv("winid")
	if winid == "" {
		fmt.Printf("FATAL: Could not find acme window. $winid not set.\n")
		os.Exit(1)
	}

	a, err := acmetools.NewAcme()
	if err != nil {
		fmt.Printf("FATAL: %s\n", err)
		os.Exit(1)
	}

	w, err := a.GetWindow(winid)
	if err != nil {
		fmt.Printf("FATAL: %s\n", err)
		os.Exit(1)
	}

	// // Using addr to find the selection address
	// 	_, _, err = w.Addr()
	// 	if err != nil {
	// 		fmt.Printf("FATAL: %s\n", err)
	// 		os.Exit(1)
	// 	}
	// 	err = w.Ctl("addr=dot")
	// 	if err != nil {
	// 		fmt.Printf("FATAL: %s\n", err)
	// 		os.Exit(1)
	// 	}
	// 	q0, q1, err := w.Addr()
	// 	if err != nil {
	// 		fmt.Printf("FATAL: %s\n", err)
	// 		os.Exit(1)
	// 	}
	//fmt.Printf("ADDR IS AT %d,%d\n", q0, q1)
	//err = w.WriteAddr(fmt.Sprintf("#0,#%d", q1))

	tag, err := w.Tag()
	if err != nil {
		fmt.Printf("FATAL: %s\n", err)
		os.Exit(1)
	}
	parts := strings.SplitN(tag, " ", 2)
	fname := parts[0]
	stat, err := os.Stat(fname)
	if err != nil {
		fmt.Printf("FATAL: %s: %s\n", fname, err)
		os.Exit(1)
	}

	lineStart := 1
	lineEnd := 1
	if !stat.IsDir() {
		// Find the line number
		lineStart, lineEnd, err = w.LineNumber()
		if err != nil {
			fmt.Printf("FATAL: %s\n", err)
			os.Exit(1)
		}
	}

	link, err := gitFileLink(fname, stat.IsDir())
	if err != nil {
		fmt.Printf("FATAL: %s\n", err)
		os.Exit(1)
	}
	if !stat.IsDir() {
		if lineStart != lineEnd {
			link = fmt.Sprintf("%s#L%d-#L%d", link, lineStart, lineEnd)
		} else {
			link = fmt.Sprintf("%s#L%d", link, lineStart)
		}
	}
	if *plumb {
		err = acmetools.Plumb("ghlink", "web", "/", link)
		if err != nil {
			fmt.Printf("FATAL: %s\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Printf("%s\n", link)
	}
}

func parseGitRemote(file, line string, dir bool) (string, error) {
	gre := regexp.MustCompile(`git@github\.com:([^/]+)/([^[:space:]]+)`)
	matches := gre.FindStringSubmatch(line)
	if matches == nil {
		return "", fmt.Errorf("No match")
	}
	owner := matches[1]
	repo := strings.TrimSuffix(matches[2], ".git")
	commit, err := currentGitCommit(file)
	if err != nil {
		return "", err
	}
	tl, err := findGitToplevel(file)
	if err != nil {
		return "", err
	}
	grpath := strings.TrimPrefix(file, tl)
	var link string
	if dir {
		link = fmt.Sprintf("https://github.com/%s/%s/tree/%s%s", owner, repo, commit, grpath)
	} else {
		link = fmt.Sprintf("https://github.com/%s/%s/blob/%s%s", owner, repo, commit, grpath)
	}
	return link, nil
}

func findGitToplevel(file string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = path.Dir(file)
	var b bytes.Buffer
	cmd.Stdout = &b
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("Failed to find top level: %w", err)
	}
	dir := strings.TrimSpace(string(b.Bytes()))
	return dir, nil
}

func currentGitCommit(file string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = path.Dir(file)
	var b bytes.Buffer
	cmd.Stdout = &b
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("Failed to find current commit hash: %w", err)
	}
	dir := strings.TrimSpace(string(b.Bytes()))
	return dir, nil
}

func gitFileLink(file string, dir bool) (string, error) {
	cmd := exec.Command("git", "remote", "-v")
	cmd.Dir = path.Dir(file)
	var b bytes.Buffer
	cmd.Stdout = &b
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	r := bufio.NewReader(&b)
	for {
		l, _, err := r.ReadLine()
		if err != nil {
			break
		}
		//fmt.Printf("L: %s\n", string(l))
		p, err := parseGitRemote(file, string(l), dir)
		if err == nil {
			return p, nil
		}
		//fmt.Printf("Err: %s\n", err)
	}
	return "", fmt.Errorf("Could not find github repository.")
}
