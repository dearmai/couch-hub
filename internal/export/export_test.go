package export

import "testing"

func TestArchivePathRejectsTraversal(t *testing.T) {
	// A vault path is client-supplied. Anything an extractor could follow out of
	// the target directory has to be refused rather than sanitised into
	// something that merely looks safe.
	rejected := []string{
		"",
		"/",
		"..",
		"../secrets.md",
		"notes/../../etc/passwd",
		`..\..\windows`,
		"notes//deep.md",
		"./notes.md",
	}
	for _, in := range rejected {
		if got := archivePath(in); got != "" {
			t.Errorf("archivePath(%q) = %q, want rejection", in, got)
		}
	}
}

func TestArchivePathKeepsOrdinaryPaths(t *testing.T) {
	cases := map[string]string{
		"note.md":                  "note.md",
		"업무/회의록 2024.md":           "업무/회의록 2024.md",
		".obsidian/plugins/x.json": ".obsidian/plugins/x.json",
		// Windows separators are how some clients spell a nested path; they are
		// a path, not a traversal.
		`folder\note.md`: "folder/note.md",
		// An absolute path is made relative rather than refused: it lands
		// inside the archive either way, and refusing it would drop a file
		// over a leading slash.
		"/etc/passwd": "etc/passwd",
	}
	for in, want := range cases {
		if got := archivePath(in); got != want {
			t.Errorf("archivePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClaimPathDisambiguatesDuplicates(t *testing.T) {
	taken := map[string]int{}

	if got := claimPath(taken, "a/note.md"); got != "a/note.md" {
		t.Fatalf("first claim = %q", got)
	}
	if got := claimPath(taken, "a/note.md"); got != "a/note (2).md" {
		t.Errorf("second claim = %q, want %q", got, "a/note (2).md")
	}
	if got := claimPath(taken, "a/note.md"); got != "a/note (3).md" {
		t.Errorf("third claim = %q, want %q", got, "a/note (3).md")
	}
	// An extensionless file must not gain one.
	if got := claimPath(taken, "LICENSE"); got != "LICENSE" {
		t.Fatalf("first LICENSE = %q", got)
	}
	if got := claimPath(taken, "LICENSE"); got != "LICENSE (2)" {
		t.Errorf("second LICENSE = %q, want %q", got, "LICENSE (2)")
	}
}
