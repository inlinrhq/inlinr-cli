package commits

import (
	"strings"
	"testing"
)

// Builds the exact bytes `git log` emits for the format in Read().
func block(sha, date string, numstat ...string) string {
	head := strings.Join([]string{sha, date}, sep)
	return recSep + head + "\n" + strings.Join(numstat, "\n") + "\n"
}

func TestParseCountsLines(t *testing.T) {
	out := block("abc123", "2026-08-04T10:00:00+02:00",
		"10\t2\tsrc/a.ts",
		"5\t0\tsrc/b.ts",
	)
	got := parse(out)
	if len(got) != 1 {
		t.Fatalf("want 1 commit, got %d", len(got))
	}
	c := got[0]
	if c.Insertions != 15 || c.Deletions != 2 || c.FilesChanged != 2 {
		t.Fatalf("want +15/-2 over 2 files, got +%d/-%d over %d",
			c.Insertions, c.Deletions, c.FilesChanged)
	}
	if c.SHA != "abc123" {
		t.Fatalf("header not parsed: %+v", c)
	}
	if c.CommittedAt.IsZero() {
		t.Fatal("timestamp not parsed")
	}
}

func TestParseBinaryFileCountsAsFileNotLines(t *testing.T) {
	// git reports "-\t-" for binaries. It is a file changed, not lines written;
	// silently folding it into the line totals would inflate them with a number
	// git deliberately refused to give.
	out := block("s", "2026-08-04T10:00:00Z",
		"-\t-\tpublic/logo.png",
		"3\t1\tsrc/a.ts",
	)
	c := parse(out)[0]
	if c.FilesChanged != 2 {
		t.Fatalf("want 2 files, got %d", c.FilesChanged)
	}
	if c.Insertions != 3 || c.Deletions != 1 {
		t.Fatalf("binary leaked into line counts: +%d/-%d", c.Insertions, c.Deletions)
	}
}

func TestParseMultiParagraphSubjectDoesNotSplitRecords(t *testing.T) {
	// A blank line inside a message is why records are separated by \x1e and not
	// by blank lines: the naive split would cut this commit in two and produce a
	// second, malformed one.
	out := block("s1", "2026-08-04T10:00:00Z", "d@e.io", "fix: a\n\nlong body here",
		"1\t1\ta.ts",
	)
	got := parse(out)
	if len(got) != 1 {
		t.Fatalf("want 1 commit, got %d", len(got))
	}
}

func TestParseMultipleCommits(t *testing.T) {
	out := block("a", "2026-08-04T10:00:00Z", "d@e.io", "one", "1\t0\ta.ts") +
		block("b", "2026-08-04T11:00:00Z", "d@e.io", "two", "2\t2\tb.ts", "4\t0\tc.ts")
	got := parse(out)
	if len(got) != 2 {
		t.Fatalf("want 2 commits, got %d", len(got))
	}
	if got[1].FilesChanged != 2 || got[1].Insertions != 6 {
		t.Fatalf("second commit wrong: %+v", got[1])
	}
}

func TestParseCommitWithNoFileChanges(t *testing.T) {
	// An empty commit still has a header and no numstat lines.
	out := recSep + strings.Join([]string{"s", "2026-08-04T10:00:00Z"}, sep) + "\n"
	got := parse(out)
	if len(got) != 1 || got[0].FilesChanged != 0 {
		t.Fatalf("want one commit with 0 files, got %+v", got)
	}
}

func TestParseToleratesGarbage(t *testing.T) {
	for _, in := range []string{
		"",
		"\n\n",
		recSep + "not-enough-fields\n",
		recSep + strings.Join([]string{"s", "not-a-date"}, sep) + "\n1\t1\ta.ts\n",
	} {
		// Must not panic, and must not invent commits from nothing.
		for _, c := range parse(in) {
			if c.SHA == "" {
				t.Fatalf("produced a commit with no sha from %q", in)
			}
		}
	}
}

func TestParseUnparseableDateDoesNotDropTheCommit(t *testing.T) {
	// A bad timestamp is worth keeping the commit for — the line counts are
	// still true. The server can fall back to now.
	out := recSep + strings.Join([]string{"sha", "nope"}, sep) + "\n2\t1\ta.ts\n"
	got := parse(out)
	if len(got) != 1 || got[0].Insertions != 2 {
		t.Fatalf("commit dropped or miscounted: %+v", got)
	}
	if !got[0].CommittedAt.IsZero() {
		t.Fatal("want zero time so the server can substitute")
	}
}
