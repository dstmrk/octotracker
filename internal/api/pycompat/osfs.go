package pycompat

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

// errnoClass restituisce la sottoclasse di OSError che Python associa a errno.
func errnoClass(errno syscall.Errno) string {
	switch errno {
	case syscall.ENOENT:
		return "FileNotFoundError"
	case syscall.EISDIR:
		return "IsADirectoryError"
	case syscall.ENOTDIR:
		return "NotADirectoryError"
	case syscall.EACCES, syscall.EPERM:
		return "PermissionError"
	}
	return "OSError"
}

// strerror restituisce il testo di strerror(errno) come lo stampa Python
// ("No such file or directory"): la tabella di Go ha gli stessi testi di
// glibc ma con l'iniziale minuscola.
func strerror(errno syscall.Errno) string {
	s := errno.Error()
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// OSError converte un errore del filesystem di Go nell'OSError Python
// equivalente: Error() è str(e), es. "[Errno 21] Is a directory: '/tmp/x'".
// Gli errori che non contengono un syscall.Errno vengono restituiti
// invariati.
func OSError(err error) error {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return err
	}
	msg := fmt.Sprintf("[Errno %d] %s", int(errno), strerror(errno))
	var pe *fs.PathError
	if errors.As(err, &pe) {
		msg += ": " + StrRepr(pe.Path)
	}
	return NewError(errnoClass(errno), msg)
}

// ignoredStatError replica pathlib._ignore_error: ENOENT, ENOTDIR, EBADF e
// ELOOP fanno restituire False a exists()/is_file() invece di sollevare.
func ignoredStatError(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case syscall.ENOENT, syscall.ENOTDIR, syscall.EBADF, syscall.ELOOP:
		return true
	}
	return false
}

// statFollow replica Path.stat() con i casi di Path.exists(): un percorso
// con byte NUL (ValueError in Python) vale come inesistente.
func statFollow(path string) (fs.FileInfo, bool, error) {
	if strings.IndexByte(path, 0) >= 0 {
		return nil, false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if ignoredStatError(err) {
			return nil, false, nil
		}
		return nil, false, OSError(err)
	}
	return info, true, nil
}

// PathExists replica Path(path).exists(): false per i file inesistenti,
// errore (PermissionError, ...) per gli altri problemi di stat.
func PathExists(path string) (bool, error) {
	_, ok, err := statFollow(path)
	return ok, err
}

// PathIsFile replica Path(path).is_file() (segue i symlink).
func PathIsFile(path string) (bool, error) {
	info, ok, err := statFollow(path)
	if !ok {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

// Getcwd replica os.getcwd(): il percorso fisico (senza symlink) della
// directory corrente, ignorando $PWD.
func Getcwd() (string, error) {
	wd, err := syscall.Getwd()
	if err != nil {
		return "", OSError(&fs.PathError{Op: "getcwd", Path: ".", Err: err})
	}
	return wd, nil
}

// RealPath replica os.path.realpath(path) (strict=False) di Python 3.13 e
// quindi Path.resolve(): risolve i symlink componente per componente; gli
// errori di lstat/readlink vengono ignorati e i componenti restanti
// aggiunti così come sono; ".." risale dopo la risoluzione dei symlink.
func RealPath(filename string) (string, error) {
	const sep = "/"
	parts := strings.Split(filename, sep)
	// rest è lo stack dei componenti da elaborare; un elemento marker
	// indica che il target di un symlink è stato risolto.
	type item struct {
		name   string
		marker bool
		link   string
	}
	rest := make([]item, 0, len(parts))
	for i := len(parts) - 1; i >= 0; i-- {
		rest = append(rest, item{name: parts[i]})
	}
	partCount := len(parts)

	var path string
	if strings.HasPrefix(filename, sep) {
		path = sep
	} else {
		wd, err := Getcwd()
		if err != nil {
			return "", err
		}
		path = wd
	}
	seen := map[string]*string{}

	for partCount > 0 {
		it := rest[len(rest)-1]
		rest = rest[:len(rest)-1]
		if it.marker {
			resolved := path
			seen[it.link] = &resolved
			continue
		}
		partCount--
		name := it.name
		if name == "" || name == "." {
			continue
		}
		if name == ".." {
			if i := strings.LastIndex(path, sep); i > 0 {
				path = path[:i]
			} else {
				path = sep
			}
			continue
		}
		var newpath string
		if path == sep {
			newpath = path + name
		} else {
			newpath = path + sep + name
		}
		info, err := os.Lstat(newpath)
		if err != nil {
			path = newpath // errore ignorato
			continue
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			path = newpath
			continue
		}
		if cached, ok := seen[newpath]; ok {
			if cached != nil {
				path = *cached
				continue
			}
			// symlink non ancora risolto: è un ciclo
			path = newpath
			continue
		}
		target, err := os.Readlink(newpath)
		if err != nil {
			path = newpath
			continue
		}
		if strings.HasPrefix(target, sep) {
			path = sep
		}
		seen[newpath] = nil
		rest = append(rest, item{marker: true, link: newpath})
		tparts := strings.Split(target, sep)
		for i := len(tparts) - 1; i >= 0; i-- {
			rest = append(rest, item{name: tparts[i]})
		}
		partCount += len(tparts)
	}
	return path, nil
}

// ReadText replica Path(path).read_text(encoding="utf-8"): legge il file,
// lo decodifica come UTF-8 stretto (UnicodeDecodeError, che è un
// ValueError) e applica i newline universali ("\r\n" e "\r" → "\n").
// Gli errori di apertura/lettura diventano OSError Python.
func ReadText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", OSError(err)
	}
	text, err := DecodeUTF8(data)
	if err != nil {
		return "", err
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n"), nil
}
