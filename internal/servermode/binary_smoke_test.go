//go:build !(android || ios)

package servermode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	buildinfo "go2tv.app/go2tv/v2"
)

func TestDesktopBinariesServeEmbeddedWebUIFromEmptyWorkingDirectory(t *testing.T) {
	commands := []string{"go2tv", "go2tv-lite"}
	for _, name := range commands {
		t.Run(name, func(t *testing.T) {
			binary := buildServerBinary(t, name)
			smokeServerBinary(t, binary)
			smokeCLIFFmpegFlag(t, binary)
			smokeServerFFmpegFlag(t, binary)
			smokeManagedChildFlag(t, binary, name == "go2tv")
			if name == "go2tv" {
				smokeManagedChildPipes(t, binary)
			}
		})
	}
}

func smokeCLIFFmpegFlag(t *testing.T, binary string) {
	t.Helper()
	missingFFmpeg := filepath.Join(t.TempDir(), "custom-ffmpeg")
	command := exec.Command(binary, "-tc", "-ffmpeg", missingFFmpeg, "-v", filepath.Join(t.TempDir(), "missing.mp4"), "-t", "http://127.0.0.1:1")
	command.Stdin = strings.NewReader("")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("missing custom ffmpeg succeeded: %q", output)
	}
	if !strings.Contains(string(output), missingFFmpeg) {
		t.Fatalf("custom ffmpeg not used: %q", output)
	}
}

func smokeServerFFmpegFlag(t *testing.T, binary string) {
	t.Helper()
	missingFFmpeg := filepath.Join(t.TempDir(), "custom-ffmpeg")
	command := exec.Command(binary, "-server", "-listen", "127.0.0.1:0", "-media-root", t.TempDir(), "-ffmpeg", missingFFmpeg)
	command.Stdin = strings.NewReader("")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("server started with missing custom ffmpeg: %q", output)
	}
	if !strings.Contains(string(output), missingFFmpeg) {
		t.Fatalf("server error does not mention custom ffmpeg: %q", output)
	}
}

// smokeManagedChildPipes runs the real binary as a managed child over actual
// anonymous stdin/stdout pipes: initial snapshot in, readiness out, EOF stops.
func smokeManagedChildPipes(t *testing.T, binary string) {
	t.Helper()
	mediaRoot := t.TempDir()
	stateDirectory := t.TempDir()
	command := exec.Command(binary, "-server", "-managed-child", "-listen", "127.0.0.1:0", "-media-root", mediaRoot)
	command.Env = append(os.Environ(), "HOME="+stateDirectory, "XDG_CONFIG_HOME="+stateDirectory)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr lockedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()

	// CRLF framing must be accepted.
	initial := "GO2TV_PARENT {\"protocol_version\":1,\"type\":\"discovery.snapshot\",\"revision\":1,\"devices\":[]}\r\n"
	if _, err := io.WriteString(stdin, initial); err != nil {
		t.Fatal(err)
	}

	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "GO2TV_MANAGED ") {
				ready <- line
				break
			}
		}
		// Drain the remainder so the child never blocks on stdout.
		for scanner.Scan() {
		}
		_ = scanner.Err()
	}()

	select {
	case line := <-ready:
		var frame struct {
			ProtocolVersion int    `json:"protocol_version"`
			Type            string `json:"type"`
			URL             string `json:"url"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "GO2TV_MANAGED ")), &frame); err != nil {
			t.Fatalf("readiness frame %q: %v", line, err)
		}
		if frame.Type != "ready" || !strings.HasPrefix(frame.URL, "http://127.0.0.1:") || strings.Contains(frame.URL, ":0/") {
			t.Fatalf("readiness frame = %+v", frame)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("no readiness frame; stderr=%s", stderr.String())
	}

	// Parent close produces EOF and a clean child exit.
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	exit := make(chan error, 1)
	go func() { exit <- command.Wait() }()
	select {
	case err := <-exit:
		if err != nil {
			t.Fatalf("managed child exit after EOF: %v; stderr=%s", err, stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("managed child did not exit on stdin EOF; stderr=%s", stderr.String())
	}
}

// smokeManagedChildFlag checks the hidden -managed-child contract: desktop
// go2tv treats it as a server-only flag and hides it from usage; go2tv-lite
// rejects it at flag parse because it never registers the flag.
func smokeManagedChildFlag(t *testing.T, binary string, desktop bool) {
	t.Helper()
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, args...)
		command.Stdin = strings.NewReader("")
		output, err := command.CombinedOutput()
		return string(output), err
	}

	output, err := run("-managed-child")
	if err == nil {
		t.Fatalf("-managed-child alone succeeded: %q", output)
	}
	if desktop {
		if !strings.Contains(output, "requires -server") {
			t.Fatalf("-managed-child without -server output = %q", output)
		}
	} else if !strings.Contains(output, "flag provided but not defined") {
		t.Fatalf("lite -managed-child output = %q", output)
	}

	usage, _ := run("-h")
	if strings.Contains(usage, "managed-child") {
		t.Fatalf("usage output exposes managed-child: %q", usage)
	}
	for _, name := range []string{"-server", "-listen", "-media-root", "-allowed-origin", "-ffmpeg", "-debug", "-v ", "-u ", "-t ", "-tc", "-l\t", "-version"} {
		if !strings.Contains(usage, strings.TrimSpace(name)) {
			t.Fatalf("usage output missing %q: %q", name, usage)
		}
	}
}

func buildServerBinary(t *testing.T, name string) string {
	t.Helper()
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	binary := filepath.Join(t.TempDir(), name)
	if goruntime.GOOS == "windows" {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-tags", "migrated_fynedo", "-o", binary, "./cmd/"+name)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, output)
	}
	return binary
}

func smokeServerBinary(t *testing.T, binary string) {
	t.Helper()
	workingDirectory := t.TempDir()
	mediaRoot := t.TempDir()
	stateDirectory := t.TempDir()
	readStdin, holdStdin, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readStdin.Close()
	defer holdStdin.Close()

	command := exec.Command(binary, "-server", "-listen", "127.0.0.1:0", "-media-root", mediaRoot)
	command.Dir = workingDirectory
	command.Stdin = readStdin
	command.Env = append(os.Environ(), "HOME="+stateDirectory, "XDG_CONFIG_HOME="+stateDirectory)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr lockedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	address := waitForServerAddress(t, stdout, command, &stderr)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second}
	baseURL := "http://" + address

	shell := getResponse(t, client, baseURL+"/", nil)
	if shell.StatusCode != http.StatusOK || !strings.Contains(shell.ContentType, "text/html") || !strings.Contains(string(shell.Body), "<title>Go2TV</title>") {
		t.Fatalf("shell status=%d type=%q body=%q", shell.StatusCode, shell.ContentType, shell.Body)
	}
	assets := regexp.MustCompile(`/assets/app\.[0-9a-f]{8}\.(?:js|css)`).FindAll(shell.Body, -1)
	if len(assets) != 2 {
		t.Fatalf("embedded asset references = %q", assets)
	}
	for _, asset := range assets {
		response := getResponse(t, client, baseURL+string(asset), nil)
		if response.StatusCode != http.StatusOK || len(response.Body) == 0 {
			t.Fatalf("asset %q status=%d bytes=%d", asset, response.StatusCode, len(response.Body))
		}
	}

	bootstrap := getResponse(t, client, baseURL+"/api/bootstrap", map[string]string{"Sec-Fetch-Site": "same-origin"})
	if bootstrap.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap status=%d body=%q", bootstrap.StatusCode, bootstrap.Body)
	}
	if len(bootstrap.Cookies) != 1 || bootstrap.Cookies[0].Name != sessionCookie || bootstrap.Cookies[0].Value == "" || bootstrap.Cookies[0].Path != "/api" || !bootstrap.Cookies[0].HttpOnly || bootstrap.Cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("bootstrap Set-Cookie = %#v", bootstrap.Cookies)
	}
	var payload struct {
		ServerVersion   string `json:"server_version"`
		ProtocolVersion int    `json:"protocol_version"`
		Roots           []any  `json:"roots"`
	}
	if err := json.Unmarshal(bootstrap.Body, &payload); err != nil {
		t.Fatalf("bootstrap JSON: %v", err)
	}
	if payload.ServerVersion != buildinfo.Version() || payload.ProtocolVersion != 1 || len(payload.Roots) != 1 {
		t.Fatalf("bootstrap = %#v", payload)
	}
	cookies := jar.Cookies(mustParseURL(t, baseURL+"/api/library"))
	if len(cookies) != 1 || cookies[0].Name != sessionCookie || cookies[0].Value == "" {
		t.Fatalf("bootstrap cookies = %#v", cookies)
	}

	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed server exited successfully")
	}
	stopped = true
	entries, err := os.ReadDir(workingDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("working directory not empty: %v", entries)
	}
}

type smokeResponse struct {
	StatusCode  int
	ContentType string
	Body        []byte
	Cookies     []*http.Cookie
}

func getResponse(t *testing.T, client *http.Client, url string, headers map[string]string) smokeResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return smokeResponse{StatusCode: response.StatusCode, ContentType: response.Header.Get("Content-Type"), Body: body, Cookies: response.Cookies()}
}

func waitForServerAddress(t *testing.T, stdout io.Reader, command *exec.Cmd, stderr *lockedBuffer) string {
	t.Helper()
	result := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			const marker = "INFO  Web server listening: "
			line := scanner.Text()
			if _, address, ok := strings.Cut(line, marker); ok {
				result <- address
				return
			}
		}
		result <- ""
	}()
	select {
	case address := <-result:
		if address == "" {
			t.Fatalf("server exited before startup: %s", stderr.String())
		}
		return address
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("server startup timeout: %s", stderr.String())
		return ""
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
