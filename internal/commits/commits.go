// Package commits reads local git history and turns it into the line counts a
// push webhook cannot provide.
//
// GitHub's push payload carries file *names*, never diff stats, so commits
// ingested from the webhook land with insertions and deletions at zero. The
// obvious fix — call the REST API for each commit — needs the `repo` OAuth
// scope, which grants full read access to every repository the user can see.
// Asking for that to count lines is out of proportion for a product whose
// pitch is that source code never leaves the machine.
//
// The machine already has the answer. `git log --numstat` produces exact
// counts locally, works for private repos, self-hosted git and any remote at
// all, and sends nothing but numbers.
package commits

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Commit is what leaves the machine: counts and a hash, nothing else.
//
// No message and no author email, on purpose. A commit message is repository
// content — client names, unreleased features, ticket titles — and nothing in
// the product reads it. The author is already known from the device token, so
// an email would be a second identifier answering no question.
//
// The rule: if no feature reads a field, it does not get sent. Anything kept
// "in case it is useful later" is something nobody weighed the cost of.
type Commit struct {
	SHA          string    `json:"sha"`
	CommittedAt  time.Time `json:"committed_at"`
	FilesChanged int       `json:"files_changed"`
	Insertions   int       `json:"insertions"`
	Deletions    int       `json:"deletions"`
}

// Field separator for the log format. ASCII unit separator: it cannot occur in
// a commit message, unlike anything printable we might otherwise pick.
const sep = "\x1f"

// Record separator, for the same reason — a commit message can contain blank
// lines, so splitting records on those would corrupt every multi-paragraph
// message.
const recSep = "\x1e"

// Read returns commits authored after `since`, newest last.
//
// `--no-merges` on purpose: a merge commit's diffstat against its first parent
// double-counts everything the branch already contributed, which would inflate
// "lines shipped" precisely when a feature lands.
func Read(dir string, since time.Time, limit int) ([]Commit, error) {
	// Hash and author date only — no %s (subject), no %ae (email).
	format := strings.Join([]string{"%H", "%aI"}, sep)
	args := []string{
		"-C", dir,
		"log",
		"--no-merges",
		"--numstat",
		"--date=iso-strict",
		fmt.Sprintf("--pretty=format:%s%s", recSep, format),
	}
	if !since.IsZero() {
		args = append(args, "--since="+since.Format(time.RFC3339))
	}
	if limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", limit))
	}

	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return parse(string(out)), nil
}

func parse(out string) []Commit {
	var commits []Commit
	for _, block := range strings.Split(out, recSep) {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		sc := bufio.NewScanner(strings.NewReader(block))
		// A commit message with a very long subject line would overflow the
		// default 64KB token limit and silently truncate the block.
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		if !sc.Scan() {
			continue
		}
		head := strings.Split(sc.Text(), sep)
		if len(head) < 2 {
			continue
		}
		at, err := time.Parse(time.RFC3339, head[1])
		if err != nil {
			at = time.Time{}
		}
		c := Commit{SHA: head[0], CommittedAt: at}

		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			// Binary files report "-\t-" rather than counts. Counting them as
			// zero-line changes is right — they are a file changed, not lines
			// written, and treating "-" as 0 via a failed parse would be
			// accidental rather than intended.
			add, addErr := strconv.Atoi(fields[0])
			del, delErr := strconv.Atoi(fields[1])
			c.FilesChanged++
			if addErr == nil {
				c.Insertions += add
			}
			if delErr == nil {
				c.Deletions += del
			}
		}
		commits = append(commits, c)
	}
	return commits
}
