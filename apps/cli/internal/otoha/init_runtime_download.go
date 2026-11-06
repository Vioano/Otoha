package otoha

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// InitRuntimeManifestURL is the matching release fallback when a package has no
// adjacent manifest. Packaging can set it with -ldflags -X.
var InitRuntimeManifestURL = "https://github.com/Vioano/Otoha/releases/download/v0.1.0-alpha.3-rc.2/runtime-manifest.json"

var executableForInitRuntime = os.Executable

var initRuntimeVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]{0,70}$`)

func ResolveInitRuntimes() (Object, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	return resolveInitRuntimes(ctx)
}

func initRuntimeLocalManifest() string {
	executable, err := executableForInitRuntime()
	if err == nil {
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		for _, directory := range []string{filepath.Dir(executable), filepath.Dir(filepath.Dir(executable))} {
			candidate := filepath.Join(directory, "runtime-manifest.json")
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				return candidate
			}
		}
	}
	return ""
}

func initRuntimeFetch(ctx context.Context, source string, maximum int64) ([]byte, error) {
	u, err := url.Parse(source)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" {
		file, err := os.Open(source)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maximum+1))
		if err == nil && int64(len(data)) > maximum {
			err = errors.New("runtime manifest exceeds size limit")
		}
		return data, err
	}
	if u.User != nil || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return nil, errors.New("runtime downloads require HTTPS")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(request *http.Request, previous []*http.Request) error {
		if len(previous) >= 10 {
			return errors.New("too many runtime download redirects")
		}
		if request.URL.Scheme != "https" && !(u.Scheme == "http" && request.URL.Scheme == "http" && (request.URL.Hostname() == "127.0.0.1" || request.URL.Hostname() == "localhost" || request.URL.Hostname() == "::1")) {
			return errors.New("runtime download redirect must retain HTTPS")
		}
		return nil
	}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runtime download returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err == nil && int64(len(data)) > maximum {
		err = errors.New("runtime download exceeds declared size")
	}
	return data, err
}

func initRuntimeContained(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", errors.New("runtime path must be relative to its package")
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("runtime path leaves its package")
	}
	return filepath.Join(root, clean), nil
}

func initRuntimePaths(root string, paths, relay Object) (Object, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	defaults := Object{"frpc": "runtimes/frpc", "surreal": "runtimes/surreal", "python": "runtimes/python/bin/python3", "server": "services/collaboration/otoha_server.py", "ca_bundle": "runtimes/ca-bundle.pem"}
	result := Object{"root": root}
	for _, key := range []string{"frpc", "surreal", "python", "server", "ca_bundle"} {
		relative := S(paths, key)
		if relative == "" {
			relative = S(defaults, key)
		}
		path, err := initRuntimeContained(root, relative)
		if err != nil {
			return nil, fmt.Errorf("runtime %s: %w", key, err)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("runtime %s is unavailable: %w", key, err)
		}
		relation, err := filepath.Rel(root, resolved)
		if err != nil || relation == ".." || strings.HasPrefix(relation, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("runtime %s resolves outside its package", key)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("runtime %s is not a regular file", key)
		}
		if key != "server" && key != "ca_bundle" && runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
			return nil, fmt.Errorf("runtime %s is not executable", key)
		}
		result[key] = path
	}
	if S(relay, "hostname") != "" {
		result["relay_hostname"] = relay["hostname"]
	}
	if S(relay, "url") != "" {
		result["relay_url"] = relay["url"]
	}
	if S(relay, "dial_ip") != "" {
		result["relay_dial_ip"] = relay["dial_ip"]
	}
	return result, nil
}

func extractInitRuntime(archive []byte, directory, archiveRoot string) error {
	reader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(header.Name, "./")
		if archiveRoot != "" {
			if name == archiveRoot || name == archiveRoot+"/" {
				continue
			}
			if !strings.HasPrefix(name, archiveRoot+"/") {
				return errors.New("runtime archive has an unexpected root")
			}
			name = strings.TrimPrefix(name, archiveRoot+"/")
		}
		if name == "" || name == "." {
			continue
		}
		if strings.HasPrefix(filepath.Base(name), "._") || filepath.Base(name) == ".DS_Store" || strings.Contains("/"+name, "/__MACOSX/") {
			continue
		}
		destination, err := initRuntimeContained(directory, name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destination, os.FileMode(header.Mode)&0777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, tarReader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(header.Linkname) {
				return errors.New("absolute runtime symlink is not supported")
			}
			target := filepath.Clean(filepath.Join(filepath.Dir(name), filepath.FromSlash(header.Linkname)))
			if _, err := initRuntimeContained(directory, target); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, destination); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader:
			continue
		default:
			return fmt.Errorf("unsupported runtime archive entry %q", name)
		}
	}
}

func resolveInitRuntimes(ctx context.Context) (Object, error) {
	if root := os.Getenv("OTOHA_RUNTIME_ROOT"); root != "" {
		return initRuntimePaths(root, nil, nil)
	}
	source := os.Getenv("OTOHA_RUNTIME_MANIFEST")
	localManifest := initRuntimeLocalManifest()
	if source == "" {
		source = localManifest
	}
	if source == "" {
		source = InitRuntimeManifestURL
	}
	data, err := initRuntimeFetch(ctx, source, 1024*1024)
	if err != nil {
		return nil, fmt.Errorf("discover packaged runtimes: %w", err)
	}
	var manifest Object
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	version := S(manifest, "version")
	if S(manifest, "schema") != "otoha-runtime/1" || !initRuntimeVersionPattern.MatchString(version) {
		return nil, errors.New("unsupported runtime manifest")
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	entry := O(O(manifest, "platforms"), platform)
	if len(entry) == 0 {
		return nil, fmt.Errorf("bundled runtimes are not available for %s", platform)
	}
	paths, relay := O(entry, "paths"), O(manifest, "relay")
	// A full runtime package can be used directly without downloading it again.
	if source == localManifest && localManifest != "" {
		if resolved, err := initRuntimePaths(filepath.Dir(localManifest), paths, relay); err == nil {
			return resolved, nil
		}
	}
	cache := filepath.Join(Home(), "runtimes", version, runtime.GOOS, runtime.GOARCH)
	if _, err := os.Stat(cache); err == nil {
		return initRuntimePaths(cache, paths, relay)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	expected := strings.ToLower(S(entry, "sha256"))
	if len(expected) != 64 || S(entry, "format") != "tar.gz" || N(entry, "bytes") <= 0 {
		return nil, errors.New("runtime manifest needs tar.gz, SHA-256 and exact byte size")
	}
	archive, err := initRuntimeFetch(ctx, S(entry, "url"), int64(N(entry, "bytes")))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(archive)
	if len(archive) != N(entry, "bytes") || hex.EncodeToString(digest[:]) != expected {
		return nil, errors.New("runtime archive checksum or byte size mismatch; nothing installed")
	}
	if err := PrivateDir(filepath.Dir(cache)); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(cache), ".otoha-runtime-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	if err := extractInitRuntime(archive, staging, S(entry, "archive_root")); err != nil {
		return nil, fmt.Errorf("extract packaged runtime: %w", err)
	}
	if _, err := initRuntimePaths(staging, paths, relay); err != nil {
		return nil, err
	}
	if err := os.Rename(staging, cache); err != nil {
		return nil, err
	}
	return initRuntimePaths(cache, paths, relay)
}
