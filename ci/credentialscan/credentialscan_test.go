// Package credentialscan holds the repository checks of P-AC3 of
// specs/SPEC-backend-completion-plugins.md: no compose file publishes a port for a database
// service, and no tracked file carries a database password.
//
// The one documented exception is the trust authentication of the two test database services
// (docker-compose.yml, POSTGRES_HOST_AUTH_METHOD): it is an authentication method, not a
// password, and this scan does not match it.
package credentialscan

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repositoryRoot is two directories above this package.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	return root
}

// trackedFiles lists the files git tracks, or, where git cannot say (no git in the
// environment, or no repository), every regular file under the root but git's own directory and
// the agents' worktrees.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	command := exec.Command("git", "ls-files", "-z")
	command.Dir = root
	if output, err := command.Output(); err == nil && len(output) > 0 {
		var files []string
		for _, name := range strings.Split(strings.TrimRight(string(output), "\x00"), "\x00") {
			files = append(files, filepath.Join(root, filepath.FromSlash(name)))
		}
		return files
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			relative, _ := filepath.Rel(root, path)
			if relative == ".git" || relative == filepath.Join(".claude", "worktrees") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	return files
}

// passwordLiterals are the shapes a database password takes in a file: the image's own password
// variable with a value, libpq's environment variable, a password key in a connection string,
// and a password in a connection URL.
var passwordLiterals = []*regexp.Regexp{
	regexp.MustCompile(`(?i)POSTGRES_PASSWORD\s*[:=]\s*["']?[^\s"'$]`),
	regexp.MustCompile(`(?i)PGPASSWORD\s*[:=]\s*["']?[^\s"'$]`),
	regexp.MustCompile(`(?i)\bpassword='?[A-Za-z0-9_\-]{3,}`),
	regexp.MustCompile(`(?i)postgres(ql)?://[^\s/:@]+:[^\s@/]+@`),
}

// TestNoTrackedFileCarriesADatabasePassword is P-AC3's scan: every tracked file, the
// specifications aside -- they quote the patterns this test refuses, as requirements -- carries
// no database password.
func TestNoTrackedFileCarriesADatabasePassword(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	self, _ := filepath.Abs("credentialscan_test.go")
	for _, path := range trackedFiles(t, root) {
		relative, _ := filepath.Rel(root, path)
		if path == self || strings.HasPrefix(filepath.ToSlash(relative), "specs/") {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue // a tracked file deleted from the working tree
		}
		if strings.ContainsRune(string(content[:min(len(content), 8000)]), 0) {
			continue // binary
		}
		for number, line := range strings.Split(string(content), "\n") {
			for _, pattern := range passwordLiterals {
				if pattern.MatchString(line) {
					t.Errorf("%s:%d carries what reads as a database password: %s", relative, number+1,
						strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestNoComposeFilePublishesADatabasePort is P-AC3's first half: a service running the
// TimescaleDB image publishes no port, in any compose file of the repository.
func TestNoComposeFilePublishesADatabasePort(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	composeFiles, err := filepath.Glob(filepath.Join(root, "docker-compose*.yml"))
	if err != nil || len(composeFiles) == 0 {
		t.Fatalf("no compose file was found (%v)", err)
	}
	databaseServices := 0
	for _, path := range composeFiles {
		for name, block := range serviceBlocks(t, path) {
			if !strings.Contains(block, "*database-base") && !strings.Contains(block, "timescale/timescaledb") {
				continue
			}
			databaseServices++
			if regexp.MustCompile(`(?m)^\s+ports:`).MatchString(block) {
				t.Errorf("%s: the database service %s publishes a port", filepath.Base(path), name)
			}
		}
	}
	if databaseServices == 0 {
		t.Error("no database service was found, so nothing was checked")
	}
}

// serviceBlocks splits a compose file's services section into one text block per service.
func serviceBlocks(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	blocks := map[string]string{}
	inServices, current := false, ""
	scanner := bufio.NewScanner(file)
	serviceName := regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*$`)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "services:":
			inServices = true
			continue
		case len(line) > 0 && line[0] != ' ' && line[0] != '#':
			inServices, current = false, ""
			continue
		}
		if !inServices {
			continue
		}
		if match := serviceName.FindStringSubmatch(line); match != nil {
			current = match[1]
			continue
		}
		if current != "" {
			blocks[current] += line + "\n"
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return blocks
}
