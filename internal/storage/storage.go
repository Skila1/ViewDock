package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var (
	ErrInvalidKey = errors.New("invalid object key")
	// ErrNotFound is wrapped by Get when the key does not exist.
	ErrNotFound = errors.New("object not found")
	// ErrListUnsupported is returned by List on a store that cannot enumerate keys.
	ErrListUnsupported = errors.New("storage provider cannot list objects")
)

type Object struct {
	Body        io.ReadCloser
	Size        int64
	ContentType string
}

type Store interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Get(context.Context, string) (Object, error)
	Delete(context.Context, string) error
}

// ObjectInfo describes one stored object returned by List.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Lister is implemented by stores that can enumerate keys under a prefix.
// Keys are returned relative to the store root, sorted ascending.
type Lister interface {
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
}

// Checker is implemented by stores that can verify their configuration and
// reachability without writing application data.
type Checker interface {
	Check(ctx context.Context) error
}

type Local struct {
	Root string
}

func NewLocal(root string) (*Local, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("local storage root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Local{Root: filepath.Clean(root)}, nil
}

func (s *Local) path(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	path := filepath.Join(s.Root, filepath.FromSlash(key))
	rel, err := filepath.Rel(s.Root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidKey
	}
	return path, nil
}

func (s *Local) Put(_ context.Context, key string, src io.Reader, size int64, _ string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vd-object-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if size >= 0 {
		src = io.LimitReader(src, size+1)
	}
	written, err := io.Copy(tmp, src)
	if err == nil && size >= 0 && written != size {
		err = fmt.Errorf("object size mismatch: got %d, want %d", written, size)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (s *Local) Get(_ context.Context, key string) (Object, error) {
	path, err := s.path(key)
	if err != nil {
		return Object{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Object{}, fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		return Object{}, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return Object{}, err
	}
	return Object{Body: f, Size: st.Size()}, nil
}

func (s *Local) Delete(_ context.Context, key string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// List walks the directory tree below Root. In-progress temporary files are
// skipped.
func (s *Local) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	start := s.Root
	if prefix != "" {
		if err := validatePrefix(prefix); err != nil {
			return nil, err
		}
		if i := strings.LastIndex(prefix, "/"); i > 0 {
			dir, err := s.path(prefix[:i])
			if err != nil {
				return nil, err
			}
			start = dir
		}
	}
	var out []ObjectInfo
	err := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".vd-object-") {
			return nil
		}
		rel, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		out = append(out, ObjectInfo{Key: key, Size: info.Size(), LastModified: info.ModTime().UTC()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Check confirms Root exists and is writable.
func (s *Local) Check(_ context.Context) error {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Root, ".vd-object-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

type S3Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
	Region    string
	PathStyle bool
}

type S3 struct {
	client *minio.Client
	bucket string
}

// NewS3 accepts Endpoint as host[:port] or as an http(s) URL without a path;
// a URL scheme overrides UseSSL.
func NewS3(cfg S3Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("s3 endpoint, bucket, access key, and secret key are required")
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if strings.Contains(endpoint, "://") {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("s3 endpoint must be host[:port] or an http(s) URL without a path")
		}
		endpoint = u.Host
		cfg.UseSSL = u.Scheme == "https"
	}
	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	}
	if cfg.PathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}
	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, err
	}
	return &S3{client: client, bucket: cfg.Bucket}, nil
}

func (s *S3) Put(ctx context.Context, key string, src io.Reader, size int64, contentType string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, src, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *S3) Get(ctx context.Context, key string) (Object, error) {
	if err := validateKey(key); err != nil {
		return Object{}, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return Object{}, err
	}
	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return Object{}, fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		return Object{}, err
	}
	return Object{Body: obj, Size: stat.Size, ContentType: stat.ContentType}, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *S3) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	if prefix != "" {
		if err := validatePrefix(prefix); err != nil {
			return nil, err
		}
	}
	var out []ObjectInfo
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out = append(out, ObjectInfo{Key: obj.Key, Size: obj.Size, LastModified: obj.LastModified.UTC()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Check confirms the credentials are accepted and the bucket exists.
func (s *S3) Check(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bucket %q does not exist", s.bucket)
	}
	return nil
}

// Prefixed scopes every key of an underlying store below a fixed prefix, so
// several features can share one bucket or directory.
type Prefixed struct {
	store  Store
	prefix string
}

// WithPrefix returns store unchanged for an empty prefix.
func WithPrefix(store Store, prefix string) (Store, error) {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return store, nil
	}
	if err := validateKey(prefix); err != nil {
		return nil, err
	}
	return &Prefixed{store: store, prefix: prefix + "/"}, nil
}

func (p *Prefixed) key(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	return p.prefix + key, nil
}

func (p *Prefixed) Put(ctx context.Context, key string, src io.Reader, size int64, contentType string) error {
	k, err := p.key(key)
	if err != nil {
		return err
	}
	return p.store.Put(ctx, k, src, size, contentType)
}

func (p *Prefixed) Get(ctx context.Context, key string) (Object, error) {
	k, err := p.key(key)
	if err != nil {
		return Object{}, err
	}
	return p.store.Get(ctx, k)
}

func (p *Prefixed) Delete(ctx context.Context, key string) error {
	k, err := p.key(key)
	if err != nil {
		return err
	}
	return p.store.Delete(ctx, k)
}

func (p *Prefixed) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	lister, ok := p.store.(Lister)
	if !ok {
		return nil, ErrListUnsupported
	}
	if prefix != "" {
		if err := validatePrefix(prefix); err != nil {
			return nil, err
		}
	}
	items, err := lister.List(ctx, p.prefix+prefix)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectInfo, 0, len(items))
	for _, it := range items {
		if rest, ok := strings.CutPrefix(it.Key, p.prefix); ok {
			it.Key = rest
			out = append(out, it)
		}
	}
	return out, nil
}

func (p *Prefixed) Check(ctx context.Context) error {
	if c, ok := p.store.(Checker); ok {
		return c.Check(ctx)
	}
	return nil
}

// validatePrefix accepts a key prefix, which may end with a slash.
func validatePrefix(prefix string) error {
	return validateKey(strings.TrimSuffix(prefix, "/"))
}

func validateKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.ContainsRune(key, '\x00') {
		return ErrInvalidKey
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(key)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return ErrInvalidKey
	}
	return nil
}
