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
)

func TestDesktopBinariesServeEmbeddedWebUIFromEmptyWorkingDirectory(t *testing.T) {
	commands := []string{"go2tv", "go2tv-lite"}
	for _, name := range commands {
		t.Run(name, func(t *testing.T) {
			binary := buildServerBinary(t, name)
			smokeServerBinary(t, binary)
		})
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
	if payload.ServerVersion != "dev" || payload.ProtocolVersion != 1 || len(payload.Roots) != 1 {
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
			if index := strings.Index(line, marker); index >= 0 {
				address := line[index+len(marker):]
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
