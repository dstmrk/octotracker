package pycompat

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type realpathGolden struct {
	Tree struct {
		Dirs  []string    `json:"dirs"`
		Files []string    `json:"files"`
		Links [][2]string `json:"links"`
	} `json:"tree"`
	RootName string `json:"root_name"`
	Cases    []struct {
		Q      string `json:"q"`
		Rel    string `json:"rel"`
		Joined string `json:"joined"`
	} `json:"cases"`
}

// TestGoldenRealPath ricrea l'albero di symlink usato da CPython e
// confronta os.path.realpath (relativo alla cwd e assoluto).
func TestGoldenRealPath(t *testing.T) {
	data, err := os.ReadFile("testdata/realpath_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g realpathGolden
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range g.Tree.Dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range g.Tree.Files {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range g.Tree.Links {
		if err := os.Symlink(strings.ReplaceAll(l[1], "@ROOT@", root), filepath.Join(root, l[0])); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	name := filepath.Base(root)
	expand := func(p string) string {
		if strings.HasPrefix(p, "@ROOT@") {
			return strings.Replace(p, "@ROOT@", root, 1)
		}
		return strings.Replace(p, "@PARENT@", filepath.Dir(root), 1)
	}
	for _, c := range g.Cases {
		q := strings.ReplaceAll(c.Q, g.RootName, name)
		got, err := RealPath(q)
		if err != nil {
			t.Fatalf("RealPath(%q): %v", q, err)
		}
		if want := expand(c.Rel); got != want {
			t.Errorf("RealPath(%q) = %q; atteso %q", q, got, want)
		}
		joined := q
		if !strings.HasPrefix(q, "/") {
			joined = root + "/" + q
		}
		got, err = RealPath(joined)
		if err != nil {
			t.Fatalf("RealPath(%q): %v", joined, err)
		}
		if want := expand(c.Joined); got != want {
			t.Errorf("RealPath(%q) = %q; atteso %q", joined, got, want)
		}
	}
}

func TestOSErrorFormatting(t *testing.T) {
	_, err := os.ReadFile("/nonexistent-dir-octotracker/x.txt")
	pe := OSError(err)
	if pe.Error() != "[Errno 2] No such file or directory: '/nonexistent-dir-octotracker/x.txt'" {
		t.Errorf("str(e) = %q", pe.Error())
	}
	if !IsInstance(pe, "FileNotFoundError") || !IsInstance(pe, "OSError") || IsInstance(pe, "ValueError") {
		t.Errorf("classe errata: %#v", pe)
	}
	dir := t.TempDir()
	_, err = os.ReadFile(dir)
	if got := OSError(err); got.Error() != "[Errno 21] Is a directory: "+StrRepr(dir) || !IsInstance(got, "IsADirectoryError") {
		t.Errorf("directory: %v", got)
	}
	cases := map[syscall.Errno]string{
		syscall.ENOTDIR: "NotADirectoryError",
		syscall.EACCES:  "PermissionError",
		syscall.EPERM:   "PermissionError",
		syscall.EIO:     "OSError",
	}
	for errno, class := range cases {
		got := OSError(&fs.PathError{Op: "open", Path: "p", Err: errno})
		if !IsInstance(got, class) {
			t.Errorf("errno %d → %v, attesa %s", errno, got, class)
		}
	}
	if got := OSError(syscall.EACCES); got.Error() != "[Errno 13] Permission denied" {
		t.Errorf("errno senza path: %q", got.Error())
	}
	plain := errors.New("altro")
	if OSError(plain) != plain {
		t.Error("errore non di sistema modificato")
	}
	if IsInstance(nil, "Exception") || !IsInstance(plain, "Exception") || IsInstance(plain, "ValueError") {
		t.Error("IsInstance su errori non Python")
	}
}

func TestPathExistsAndIsFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	loop := filepath.Join(dir, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path           string
		exists, isFile bool
	}{
		{file, true, true},
		{dir, true, false},
		{filepath.Join(dir, "missing"), false, false},
		{filepath.Join(file, "sub"), false, false}, // ENOTDIR
		{loop, false, false},                       // ELOOP
		{"a\x00b", false, false},                   // ValueError: embedded null byte
	}
	for _, tt := range tests {
		ex, err := PathExists(tt.path)
		if err != nil || ex != tt.exists {
			t.Errorf("PathExists(%q) = %v, %v", tt.path, ex, err)
		}
		isf, err := PathIsFile(tt.path)
		if err != nil || isf != tt.isFile {
			t.Errorf("PathIsFile(%q) = %v, %v", tt.path, isf, err)
		}
	}
	long := filepath.Join(dir, strings.Repeat("x", 300))
	if _, err := PathExists(long); err == nil || !strings.HasPrefix(err.Error(), "[Errno 36] File name too long: ") {
		t.Errorf("ENAMETOOLONG: %v", err)
	}
	if _, err := PathIsFile(long); err == nil {
		t.Error("PathIsFile ENAMETOOLONG senza errore")
	}
}

func TestReadText(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "m.txt")
	if err := os.WriteFile(p, []byte("a\r\nb\rc\n\xef\xbb\xbfd"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadText(p)
	if err != nil || got != "a\nb\nc\n\xef\xbb\xbfd" {
		t.Errorf("ReadText = %q, %v", got, err)
	}
	bad := filepath.Join(dir, "bad.txt")
	if err := os.WriteFile(bad, []byte("ok\xff"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadText(bad); err == nil || !IsInstance(err, "ValueError") ||
		err.Error() != "'utf-8' codec can't decode byte 0xff in position 2: invalid start byte" {
		t.Errorf("ReadText(bad) = %v", err)
	}
	if _, err := ReadText(dir); err == nil || !IsInstance(err, "IsADirectoryError") {
		t.Errorf("ReadText(dir) = %v", err)
	}
}

func TestGetcwdAndRelativeRealPath(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(link)
	t.Setenv("PWD", link) // os.getcwd() di Python ignora $PWD
	wd, err := Getcwd()
	if err != nil || wd != root {
		t.Errorf("Getcwd = %q, %v; atteso %q", wd, err, root)
	}
	got, err := RealPath("x/../y")
	if err != nil || got != root+"/y" {
		t.Errorf("RealPath = %q, %v", got, err)
	}
}
