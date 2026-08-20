package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/exercism/cli/config"
	"github.com/exercism/cli/workspace"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestDownloadWithoutToken(t *testing.T) {
	cfg := config.Config{
		UserViperConfig: viper.New(),
	}

	err := runDownload(cfg, pflag.NewFlagSet("fake", pflag.PanicOnError), []string{})
	if assert.Error(t, err) {
		assert.Regexp(t, "Welcome to Exercism", err.Error())
		// It uses the default base API url to infer the host
		assert.Regexp(t, "exercism.org/settings", err.Error())
	}
}

func TestDownloadWithoutWorkspace(t *testing.T) {
	v := viper.New()
	v.Set("token", "abc123")
	cfg := config.Config{
		UserViperConfig: v,
	}

	err := runDownload(cfg, pflag.NewFlagSet("fake", pflag.PanicOnError), []string{})
	if assert.Error(t, err) {
		assert.Regexp(t, "re-run the configure", err.Error())
	}
}

func TestDownloadWithoutBaseURL(t *testing.T) {
	v := viper.New()
	v.Set("token", "abc123")
	v.Set("workspace", "/home/whatever")
	cfg := config.Config{
		UserViperConfig: v,
	}

	err := runDownload(cfg, pflag.NewFlagSet("fake", pflag.PanicOnError), []string{})
	if assert.Error(t, err) {
		assert.Regexp(t, "re-run the configure", err.Error())
	}
}

func TestDownloadWithoutFlags(t *testing.T) {
	v := viper.New()
	v.Set("token", "abc123")
	v.Set("workspace", "/home/username")
	v.Set("apibaseurl", "http://example.com")

	cfg := config.Config{
		UserViperConfig: v,
	}

	flags := pflag.NewFlagSet("fake", pflag.PanicOnError)
	setupDownloadFlags(flags)

	err := runDownload(cfg, flags, []string{})
	if assert.Error(t, err) {
		assert.Regexp(t, "need an --exercise name or a solution --uuid", err.Error())
	}
}

func TestSolutionFile(t *testing.T) {
	testCases := []struct {
		name, file, expectedPath, expectedURL string
	}{
		{
			name:         "filename with special character",
			file:         "special-char-filename#.txt",
			expectedPath: "special-char-filename#.txt",
			expectedURL:  "http://www.example.com/special-char-filename%23.txt",
		},
		{
			name:         "filename with leading slash",
			file:         "/with-leading-slash.txt",
			expectedPath: fmt.Sprintf("%cwith-leading-slash.txt", os.PathSeparator),
			expectedURL:  "http://www.example.com//with-leading-slash.txt",
		},
		{
			name:         "filename with leading backslash",
			file:         "\\with-leading-backslash.txt",
			expectedPath: fmt.Sprintf("%cwith-leading-backslash.txt", os.PathSeparator),
			expectedURL:  "http://www.example.com/%5Cwith-leading-backslash.txt",
		},
		{
			name:         "filename with backslashes in path",
			file:         "\\backslashes\\in-path.txt",
			expectedPath: fmt.Sprintf("%[1]cbackslashes%[1]cin-path.txt", os.PathSeparator),
			expectedURL:  "http://www.example.com/%5Cbackslashes%5Cin-path.txt",
		},
		{
			name:         "path with a numeric suffix",
			file:         "/bogus-exercise-12345/numeric.txt",
			expectedPath: fmt.Sprintf("%[1]cbogus-exercise-12345%[1]cnumeric.txt", os.PathSeparator),
			expectedURL:  "http://www.example.com//bogus-exercise-12345/numeric.txt",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sf := solutionFile{
				path:    tc.file,
				baseURL: "http://www.example.com/",
			}

			if sf.relativePath() != tc.expectedPath {
				t.Fatalf("Expected path '%s', got '%s'", tc.expectedPath, sf.relativePath())
			}

			url, err := sf.url()
			if err != nil {
				t.Fatal(err)
			}

			if url != tc.expectedURL {
				t.Fatalf("Expected URL '%s', got '%s'", tc.expectedURL, url)
			}
		})
	}
}

func TestDownload(t *testing.T) {
	co := newCapturedOutput()
	co.override()
	defer co.reset()

	testCases := []struct {
		requester   bool
		expectedDir string
		flags       map[string]string
	}{
		{
			requester:   true,
			expectedDir: "",
			flags:       map[string]string{"exercise": "bogus-exercise"},
		},
		{
			requester:   true,
			expectedDir: "",
			flags:       map[string]string{"uuid": "bogus-id"},
		},
		{
			requester:   false,
			expectedDir: filepath.Join("users", "alice"),
			flags:       map[string]string{"uuid": "bogus-id"},
		},
	}

	for _, tc := range testCases {
		tmpDir, err := os.MkdirTemp("", "download-cmd")
		defer os.RemoveAll(tmpDir)
		assert.NoError(t, err)

		ts := fakeDownloadServer(strconv.FormatBool(tc.requester))
		defer ts.Close()

		v := viper.New()
		v.Set("workspace", tmpDir)
		v.Set("apibaseurl", ts.URL)
		v.Set("token", "abc123")

		cfg := config.Config{
			UserViperConfig: v,
		}
		flags := pflag.NewFlagSet("fake", pflag.PanicOnError)
		setupDownloadFlags(flags)
		for name, value := range tc.flags {
			flags.Set(name, value)
		}

		err = runDownload(cfg, flags, []string{})
		assert.NoError(t, err)

		targetDir := filepath.Join(tmpDir, tc.expectedDir)
		assertDownloadedCorrectFiles(t, targetDir)

		dir := filepath.Join(targetDir, "bogus-track", "bogus-exercise")
		b, err := os.ReadFile(workspace.NewExerciseFromDir(dir).MetadataFilepath())
		assert.NoError(t, err)
		var metadata workspace.ExerciseMetadata
		err = json.Unmarshal(b, &metadata)
		assert.NoError(t, err)

		assert.Equal(t, "bogus-track", metadata.Track)
		assert.Equal(t, "bogus-exercise", metadata.ExerciseSlug)
		assert.Equal(t, tc.requester, metadata.IsRequester)
	}
}

func TestDownloadToExistingDirectory(t *testing.T) {
	co := newCapturedOutput()
	co.override()
	defer co.reset()

	testCases := []struct {
		exerciseDir string
		flags       map[string]string
	}{
		{
			exerciseDir: filepath.Join("bogus-track", "bogus-exercise"),
			flags:       map[string]string{"exercise": "bogus-exercise", "track": "bogus-track"},
		},
	}

	for _, tc := range testCases {
		tmpDir, err := os.MkdirTemp("", "download-cmd")
		defer os.RemoveAll(tmpDir)
		assert.NoError(t, err)

		err = os.MkdirAll(filepath.Join(tmpDir, tc.exerciseDir), os.FileMode(0755))
		assert.NoError(t, err)

		ts := fakeDownloadServer("true")
		defer ts.Close()

		v := viper.New()
		v.Set("workspace", tmpDir)
		v.Set("apibaseurl", ts.URL)
		v.Set("token", "abc123")

		cfg := config.Config{
			UserViperConfig: v,
		}
		flags := pflag.NewFlagSet("fake", pflag.PanicOnError)
		setupDownloadFlags(flags)
		for name, value := range tc.flags {
			flags.Set(name, value)
		}

		err = runDownload(cfg, flags, []string{})

		if assert.Error(t, err) {
			assert.Regexp(t, "directory '.+' already exists", err.Error())
		}
	}
}

func TestDownloadToExistingDirectoryWithForce(t *testing.T) {
	co := newCapturedOutput()
	co.override()
	defer co.reset()

	testCases := []struct {
		exerciseDir string
		flags       map[string]string
	}{
		{
			exerciseDir: filepath.Join("bogus-track", "bogus-exercise"),
			flags:       map[string]string{"exercise": "bogus-exercise", "track": "bogus-track"},
		},
	}

	for _, tc := range testCases {
		tmpDir, err := os.MkdirTemp("", "download-cmd")
		defer os.RemoveAll(tmpDir)
		assert.NoError(t, err)

		err = os.MkdirAll(filepath.Join(tmpDir, tc.exerciseDir), os.FileMode(0755))
		assert.NoError(t, err)

		ts := fakeDownloadServer("true")
		defer ts.Close()

		v := viper.New()
		v.Set("workspace", tmpDir)
		v.Set("apibaseurl", ts.URL)
		v.Set("token", "abc123")

		cfg := config.Config{
			UserViperConfig: v,
		}
		flags := pflag.NewFlagSet("fake", pflag.PanicOnError)
		setupDownloadFlags(flags)
		for name, value := range tc.flags {
			flags.Set(name, value)
		}
		flags.Set("force", "true")

		err = runDownload(cfg, flags, []string{})
		assert.NoError(t, err)
	}
}

func fakeDownloadServer(requestor string) *httptest.Server {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)

	mux.HandleFunc("/file-1.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "this is file 1")
	})

	mux.HandleFunc("/subdir/file-2.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "this is file 2")
	})

	mux.HandleFunc("/file-3.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "")
	})

	mux.HandleFunc("/solutions/latest", func(w http.ResponseWriter, r *http.Request) {
		payloadBody := fmt.Sprintf(payloadTemplate, requestor, server.URL+"/")
		fmt.Fprint(w, payloadBody)
	})
	mux.HandleFunc("/solutions/bogus-id", func(w http.ResponseWriter, r *http.Request) {
		payloadBody := fmt.Sprintf(payloadTemplate, requestor, server.URL+"/")
		fmt.Fprint(w, payloadBody)
	})

	return server
}

func assertDownloadedCorrectFiles(t *testing.T, targetDir string) {
	expectedFiles := []struct {
		desc     string
		path     string
		contents string
	}{
		{
			desc:     "a file in the exercise root directory",
			path:     filepath.Join(targetDir, "bogus-track", "bogus-exercise", "file-1.txt"),
			contents: "this is file 1",
		},
		{
			desc:     "a file in a subdirectory",
			path:     filepath.Join(targetDir, "bogus-track", "bogus-exercise", "subdir", "file-2.txt"),
			contents: "this is file 2",
		},
	}

	for _, file := range expectedFiles {
		t.Run(file.desc, func(t *testing.T) {
			b, err := os.ReadFile(file.path)
			assert.NoError(t, err)
			assert.Equal(t, file.contents, string(b))
		})
	}

	path := filepath.Join(targetDir, "bogus-track", "bogus-exercise", "file-3.txt")
	_, err := os.Lstat(path)
	assert.NoError(t, err)
}

func TestDownloadError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error": {"type": "error", "message": "test error"}}`)
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	tmpDir, err := os.MkdirTemp("", "submit-err-tmp-dir")
	defer os.RemoveAll(tmpDir)
	assert.NoError(t, err)

	v := viper.New()
	v.Set("token", "abc123")
	v.Set("workspace", tmpDir)
	v.Set("apibaseurl", ts.URL)

	cfg := config.Config{
		Persister:       config.InMemoryPersister{},
		UserViperConfig: v,
		DefaultBaseURL:  "http://example.com",
	}

	flags := pflag.NewFlagSet("fake", pflag.PanicOnError)
	setupDownloadFlags(flags)
	flags.Set("uuid", "value")

	err = runDownload(cfg, flags, []string{})

	assert.Equal(t, `expected response with Content-Type "application/json" but got status "400 Bad Request" with Content-Type "text/plain; charset=utf-8"`, err.Error())

}

const payloadTemplate = `
{
	"solution": {
		"id": "bogus-id",
		"user": {
			"handle": "alice",
			"is_requester": %s
		},
		"exercise": {
			"id": "bogus-exercise",
			"instructions_url": "http://example.com/bogus-exercise",
			"auto_approve": false,
			"track": {
				"id": "bogus-track",
				"language": "Bogus Language"
			}
		},
		"file_download_base_url": "%s",
		"files": [
			"file-1.txt",
			"subdir/file-2.txt",
			"file-3.txt"
		],
		"iteration": {
			"submitted_at": "2017-08-21t10:11:12.130z"
		}
	}
}
`

func TestDownloadRetriesRateLimitedFiles(t *testing.T) {
	co := newCapturedOutput()
	co.override()
	defer co.reset()

	var delays []time.Duration
	oldSleep := retrySleep
	retrySleep = func(d time.Duration) { delays = append(delays, d) }
	defer func() { retrySleep = oldSleep }()

	// Rate limit every file once, then serve it.
	rateLimited := map[string]bool{}
	ts := fakeDownloadServerWithFileHandler(func(w http.ResponseWriter, r *http.Request, body string) bool {
		if rateLimited[r.URL.Path] {
			return false
		}
		rateLimited[r.URL.Path] = true
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, "Retry later")
		return true
	})
	defer ts.Close()

	tmpDir, err := os.MkdirTemp("", "download-cmd")
	defer os.RemoveAll(tmpDir)
	assert.NoError(t, err)

	err = runDownload(downloadConfig(tmpDir, ts.URL), downloadFlags(map[string]string{"uuid": "bogus-id"}), []string{})
	assert.NoError(t, err)

	assertDownloadedCorrectFiles(t, tmpDir)
	assert.Equal(t, []time.Duration{2 * time.Second, 2 * time.Second, 2 * time.Second}, delays)
}

func TestDownloadFailsWhenPersistentlyRateLimited(t *testing.T) {
	co := newCapturedOutput()
	co.override()
	defer co.reset()

	oldSleep := retrySleep
	retrySleep = func(time.Duration) {}
	defer func() { retrySleep = oldSleep }()

	var requests int
	ts := fakeDownloadServerWithFileHandler(func(w http.ResponseWriter, r *http.Request, body string) bool {
		requests++
		w.Header().Set("Retry-After", "23")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, "Retry later")
		return true
	})
	defer ts.Close()

	tmpDir, err := os.MkdirTemp("", "download-cmd")
	defer os.RemoveAll(tmpDir)
	assert.NoError(t, err)

	err = runDownload(downloadConfig(tmpDir, ts.URL), downloadFlags(map[string]string{"uuid": "bogus-id"}), []string{})
	if assert.Error(t, err) {
		assert.Regexp(t, "failed to download 'file-1.txt'", err.Error())
		assert.Regexp(t, "please try again after 23 seconds", err.Error())
	}
	assert.Equal(t, maxDownloadAttempts, requests)

	// A rate limited download must not leave an empty file behind.
	_, err = os.Lstat(filepath.Join(tmpDir, "bogus-track", "bogus-exercise", "file-1.txt"))
	assert.True(t, os.IsNotExist(err))
}

func TestDownloadFailsOnMissingFile(t *testing.T) {
	co := newCapturedOutput()
	co.override()
	defer co.reset()

	ts := fakeDownloadServerWithFileHandler(func(w http.ResponseWriter, r *http.Request, body string) bool {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Not found")
		return true
	})
	defer ts.Close()

	tmpDir, err := os.MkdirTemp("", "download-cmd")
	defer os.RemoveAll(tmpDir)
	assert.NoError(t, err)

	err = runDownload(downloadConfig(tmpDir, ts.URL), downloadFlags(map[string]string{"uuid": "bogus-id"}), []string{})
	if assert.Error(t, err) {
		assert.Regexp(t, "failed to download 'file-1.txt'", err.Error())
		assert.Regexp(t, "404 Not Found", err.Error())
	}
}

func TestRetryDelay(t *testing.T) {
	testCases := []struct {
		name          string
		status        int
		retryAfter    string
		attempt       int
		expectedDelay time.Duration
		retryable     bool
	}{
		{
			name:          "rate limited with delay seconds",
			status:        http.StatusTooManyRequests,
			retryAfter:    "23",
			attempt:       1,
			expectedDelay: 23 * time.Second,
			retryable:     true,
		},
		{
			name:          "rate limited without a Retry-After header backs off",
			status:        http.StatusTooManyRequests,
			attempt:       2,
			expectedDelay: 2 * defaultRetryDelay,
			retryable:     true,
		},
		{
			name:          "a Retry-After date in the past means retry now",
			status:        http.StatusServiceUnavailable,
			retryAfter:    "Mon, 02 Jan 2006 15:04:05 GMT",
			attempt:       1,
			expectedDelay: 0,
			retryable:     true,
		},
		{
			name:      "other errors are not retryable",
			status:    http.StatusNotFound,
			attempt:   1,
			retryable: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res := &http.Response{StatusCode: tc.status, Header: make(http.Header)}
			if tc.retryAfter != "" {
				res.Header.Set("Retry-After", tc.retryAfter)
			}

			delay, retryable := retryDelay(res, tc.attempt)
			assert.Equal(t, tc.retryable, retryable)
			if tc.retryable {
				assert.Equal(t, tc.expectedDelay, delay)
			}
		})
	}
}

func downloadConfig(workspaceDir, baseURL string) config.Config {
	v := viper.New()
	v.Set("workspace", workspaceDir)
	v.Set("apibaseurl", baseURL)
	v.Set("token", "abc123")
	return config.Config{UserViperConfig: v}
}

func downloadFlags(values map[string]string) *pflag.FlagSet {
	flags := pflag.NewFlagSet("fake", pflag.PanicOnError)
	setupDownloadFlags(flags)
	for name, value := range values {
		flags.Set(name, value)
	}
	return flags
}

// fakeDownloadServerWithFileHandler serves the same solution as
// fakeDownloadServer, but lets a test intercept the individual file requests.
// The interceptor returns true when it has handled the response itself.
func fakeDownloadServerWithFileHandler(handler func(w http.ResponseWriter, r *http.Request, body string) bool) *httptest.Server {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)

	files := map[string]string{
		"/file-1.txt":        "this is file 1",
		"/subdir/file-2.txt": "this is file 2",
		"/file-3.txt":        "",
	}
	for path, body := range files {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if handler(w, r, body) {
				return
			}
			fmt.Fprint(w, body)
		})
	}

	mux.HandleFunc("/solutions/bogus-id", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, payloadTemplate, "true", server.URL+"/")
	})

	return server
}
