package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"github.com/knusbaum/acmetools"
)

func main() {
	// 	npc, err := dialAcme()
	// 	if err != nil {
	// 		fmt.Printf("FATAL: %s\n", err)
	// 		os.Exit(1)
	// 	}

	winid := os.Getenv("winid")
	if winid == "" {
		fmt.Printf("FATAL: Could not find acme window. $winid not set.\n")
		os.Exit(1)
	}
	// 	fmt.Printf("Opening /%s/ctl\n", winid)
	// 	f, err := npc.Open(fmt.Sprintf("/%s/ctl", winid), proto.Ordwr)
	// 	if err != nil {
	// 		fmt.Printf("Tried to read index, but failed: %s\n", err)
	// 		os.Exit(1)
	// 	}
	// 	defer f.Close()
	//
	// 	f2, err := npc.Open(fmt.Sprintf("/%s/data", winid), proto.Ordwr)
	// 	if err != nil {
	// 		fmt.Printf("Tried to read index, but failed: %s\n", err)
	// 		os.Exit(1)
	// 	}
	// 	defer f2.Close()
	//
	// 	f3, err := npc.Open(fmt.Sprintf("/%s/addr", winid), proto.Ordwr)
	// 	if err != nil {
	// 		fmt.Printf("Tried to read index, but failed: %s\n", err)
	// 		os.Exit(1)
	// 	}
	// 	defer f3.Close()
	// 	//io.WriteString(f3, "mount.")
	// 	//io.WriteString(f3, ".")
	//
	// 	_, err = io.WriteString(f, "addr=dot\n")
	// 	if err != nil {
	// 		fmt.Printf("While writing ctl: FATAL: %s\n", err)
	// 		os.Exit(1)
	// 	}
	//
	// 	io.Copy(os.Stdout, f3)
	// 	//io.Copy(os.Stdout, f2)

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

	_, _, err = w.Addr()
	if err != nil {
		fmt.Printf("FATAL: %s\n", err)
		os.Exit(1)
	}

	err = w.Ctl("addr=dot")
	if err != nil {
		fmt.Printf("FATAL: %s\n", err)
		os.Exit(1)
	}

	// 	q0, q1, err := w.Addr()
	// 	if err != nil {
	// 		fmt.Printf("FATAL: %s\n", err)
	// 		os.Exit(1)
	// 	}

	//fmt.Printf("ADDR IS AT %d,%d\n", q0, q1)

	//err = w.WriteAddr(fmt.Sprintf("#0,#%d", q1))
	err = w.WriteAddr("0,.")
	if err != nil {
		fmt.Printf("FATAL: %s\n", err)
		os.Exit(1)
	}

	xdata, err := w.XData()
	if err != nil {
		fmt.Printf("FATAL: %s\n", err)
		os.Exit(1)
	}

	lines := 1
	r := bufio.NewReader(xdata)
	for {
		_, err := r.ReadSlice('\n')
		//fmt.Printf("l: [%s], err: %v\n", l, err)
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Printf("FATAL: %s\n", err)
			os.Exit(1)
		}
		lines++
	}
	fmt.Printf("Line %d\n", lines)

	tag, err := w.Tag()
	if err != nil {
		fmt.Printf("FATAL: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("tag: [%s]\n", tag)
	parts := strings.SplitN(tag, " ", 2)
	fname := parts[0]
	if _, err := os.Stat(fname); err != nil {
		fmt.Printf("%s: %s\n", fname, err)
		os.Exit(1)
	}

	link, err := findGitRemote(fname)
	if err != nil {
		fmt.Printf("FATAL: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("Link: %s\n", link)
}

func parseGitRemote(file, line string) (string, error) {
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
	link := fmt.Sprintf("https://github.com/%s/%s/commit/%s/%s", owner, repo, commit, grpath)
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
	dir := string(b.Bytes())
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
	dir := string(b.Bytes())
	return dir, nil
}

func findGitRemote(file string) (string, error) {
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
		fmt.Printf("L: %s\n", string(l))
		p, err := parseGitRemote(file, string(l))
		if err == nil {
			return p, nil
		}
		fmt.Printf("Err: %s\n", err)
	}
	return "", fmt.Errorf("Could not find github repository.")
}
