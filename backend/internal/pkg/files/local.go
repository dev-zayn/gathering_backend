package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Local is a laptop's stand-in for the bucket: a directory, and the api
// serving it under /files/. Nothing is signed, because nothing on a laptop
// is a secret from the person running it. Never in production, which conf
// refuses.
type Local struct {
	dir    string
	public string
}

// NewLocal makes the directory and answers links under publicURL. The
// directory is the api's own; a key is a path inside it and nothing else.
func NewLocal(dir, publicURL string) (*Local, error) {
	if dir == "" {
		return nil, fmt.Errorf("files: no directory given")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("files: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("files: %w", err)
	}
	return &Local{dir: abs, public: strings.TrimRight(publicURL, "/")}, nil
}

// errBadKey is a key that would reach outside the directory. A key is made
// by us, so one that does not is a bug and not a request to honour.
var errBadKey = errors.New("files: key is not a path inside the directory")

// place is the file a key names, refusing anything that would climb out.
func (l *Local) place(key string) (string, error) {
	clean := path.Clean("/" + key)
	if key == "" || strings.HasSuffix(key, "/") || clean == "/" {
		return "", errBadKey
	}
	for _, part := range strings.Split(key, "/") {
		if part == ".." {
			return "", errBadKey
		}
	}
	return filepath.Join(l.dir, filepath.FromSlash(clean)), nil
}

func (l *Local) Put(_ context.Context, key, _ string, data []byte) error {
	file, err := l.place(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return fmt.Errorf("files: put %s: %w", key, err)
	}
	// Whole, then renamed: a reader never sees half a picture.
	tmp := file + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("files: put %s: %w", key, err)
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("files: put %s: %w", key, err)
	}
	return nil
}

// URL is the plain address under /files/. The ttl is ignored: there is no
// signature to expire.
func (l *Local) URL(_ context.Context, key string, _ time.Duration) (string, error) {
	if _, err := l.place(key); err != nil {
		return "", err
	}
	return l.public + "/files/" + key, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	file, err := l.place(key)
	if err != nil {
		return err
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("files: delete %s: %w", key, err)
	}
	return nil
}

// Handler serves the directory under /files/. Files only: a directory is not
// found, so nothing lists what is there.
func (l *Local) Handler() http.Handler {
	server := http.FileServer(http.FS(noListing{os.DirFS(l.dir)}))
	return http.StripPrefix("/files/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// A link is drawn from, not navigated to; and what is here is only
		// ever a picture. The cache is the browser's for the hour a signed
		// link would have lived.
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		server.ServeHTTP(w, r)
	}))
}

// noListing is the directory with every directory in it not found, which is
// what stops http.FileServer printing an index.
type noListing struct{ fs.FS }

func (n noListing) Open(name string) (fs.File, error) {
	f, err := n.FS.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}
