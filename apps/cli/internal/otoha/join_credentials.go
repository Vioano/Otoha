package otoha

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

var installedJoinExecutable = InstalledExecutable

func credentialOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func giteaProfileURL(profile Object) (*url.URL, error) {
	u, err := url.Parse(S(profile, "url"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || strings.Contains(u.Path, "..") {
		return nil, errors.New("Gitea profile requires a credential-free HTTP(S) URL")
	}
	return u, nil
}

func giteaRepositoryPath(base *url.URL, path string) (string, error) {
	prefix := strings.Trim(base.Path, "/")
	path = strings.TrimPrefix(path, "/")
	if prefix != "" {
		if !strings.HasPrefix(path, prefix+"/") {
			return "", errors.New("repository is outside the Gitea profile path")
		}
		path = strings.TrimPrefix(path, prefix+"/")
	}
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !aliasRE.MatchString(parts[0]) || !aliasRE.MatchString(parts[1]) || parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return "", errors.New("repository must identify exactly OWNER/REPO")
	}
	return strings.Join(parts, "/"), nil
}

func joinedGiteaProfile(path, repositoryURL string) (string, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	profile, err := Load(absolute)
	if err != nil {
		return "", "", err
	}
	base, err := giteaProfileURL(profile)
	if err != nil {
		return "", "", err
	}
	u, err := url.Parse(repositoryURL)
	if err != nil || u.RawPath != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || credentialOrigin(u) != credentialOrigin(base) {
		return "", "", errors.New("repository origin does not match the existing Gitea profile; credentials were not used")
	}
	repository, err := giteaRepositoryPath(base, u.Path)
	if err != nil {
		return "", "", err
	}
	if S(profile, "username") == "" || (S(profile, "token") == "" && S(profile, "password") == "") {
		return "", "", errors.New("existing Gitea profile has no usable username and token/password")
	}
	return absolute, repository, nil
}

// GiteaCredential implements Git's credential helper protocol. Its scope is a
// single repository in an existing local profile. It never stores or erases
// credentials and does not read profiles for non-get operations.
func GiteaCredential(args []string, input io.Reader, output io.Writer) error {
	f := flag.NewFlagSet("gitea-credential", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	profilePath := f.String("profile", "", "existing private Gitea profile")
	repository := f.String("repository", "", "authorized OWNER/REPO")
	if err := f.Parse(args); err != nil {
		return err
	}
	if len(f.Args()) != 1 || f.Args()[0] != "get" {
		return nil
	}
	if *profilePath == "" || *repository == "" {
		return errors.New("credential helper requires --profile and --repository")
	}
	fields := map[string]string{}
	scan := bufio.NewScanner(input)
	scan.Buffer(make([]byte, 1024), 16384)
	for scan.Scan() {
		if scan.Text() == "" {
			break
		}
		key, value, ok := strings.Cut(scan.Text(), "=")
		if ok {
			fields[key] = value
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	profile, err := Load(*profilePath)
	if err != nil {
		return err
	}
	base, err := giteaProfileURL(profile)
	if err != nil {
		return err
	}
	request, err := url.Parse(fields["protocol"] + "://" + fields["host"])
	if err != nil || request.User != nil || request.Path != "" || request.RawQuery != "" || request.Fragment != "" || credentialOrigin(request) != credentialOrigin(base) {
		return nil
	}
	requestedRepository, err := giteaRepositoryPath(base, fields["path"])
	if err != nil || requestedRepository != *repository {
		return nil
	}
	username := S(profile, "username")
	password := S(profile, "token")
	if password == "" {
		password = S(profile, "password")
	}
	if username == "" || password == "" || strings.ContainsAny(username+password, "\r\n\x00") || (fields["username"] != "" && fields["username"] != username) {
		return nil
	}
	_, err = fmt.Fprintf(output, "username=%s\npassword=%s\n\n", username, password)
	return err
}

func joinGiteaHelper(profile, repositoryURL string) (string, error) {
	absolute, repository, err := joinedGiteaProfile(profile, repositoryURL)
	if err != nil {
		return "", err
	}
	executable, _, err := installedJoinExecutable()
	if err != nil {
		return "", err
	}
	return "!" + shellQuote(executable) + " gitea-credential --profile " + shellQuote(absolute) + " --repository " + shellQuote(repository), nil
}

func saveJoinCredentialHelper(ctx context.Context, directory, helper string) error {
	for _, args := range [][]string{
		{"--replace-all", "credential.helper", ""}, {"--add", "credential.helper", helper},
		{"credential.useHttpPath", "true"}, {"http.followRedirects", "false"},
	} {
		if err := exec.CommandContext(ctx, "git", append([]string{"-C", directory, "config", "--local"}, args...)...).Run(); err != nil {
			return fmt.Errorf("repository exists but local credential helper configuration failed: %w", err)
		}
	}
	return nil
}
