package steps

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/update"
	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/cucumber/godog"
)

const updateOriginBinary = "OLD-BINARY\n"

func releaseAsset() string {
	name := "colt-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func startReleaseServer(tag string, binary []byte, tamper bool) *httptest.Server {
	asset := releaseAsset()
	sum := sha256.Sum256(binary)
	if tamper {
		sum[0] ^= 0xff
	}
	checksums := hex.EncodeToString(sum[:]) + "  " + asset + "\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/"+tag, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})
	mux.HandleFunc("/releases/download/"+tag+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(binary)
	})
	return httptest.NewServer(mux)
}

func RegisterUpdateSteps(ctx *godog.ScenarioContext, w *fixture.World) {
	setup := func(tag string, binary []byte, tamper bool) error {
		w.ReleaseServer = startReleaseServer(tag, binary, tamper)
		w.UpdateTarget = filepath.Join(w.Dir, "current-colt")
		if err := os.WriteFile(w.UpdateTarget, []byte(updateOriginBinary), 0o755); err != nil {
			return err
		}
		w.Updater = &update.Client{
			HTTP:        &http.Client{},
			BaseURL:     w.ReleaseServer.URL,
			GOOS:        runtime.GOOS,
			GOARCH:      runtime.GOARCH,
			CurrentPath: w.UpdateTarget,
			Version:     "v0.4.0",
		}
		return nil
	}

	ctx.Step(`^the release channel advertises latest "([^"]*)"$`, func(tag string) error {
		return setup(tag, nil, false)
	})
	ctx.Step(`^the release channel advertises latest "([^"]*)" with a valid artifact$`, func(tag string) error {
		return setup(tag, []byte("NEW-BINARY-"+tag+"\n"), false)
	})
	ctx.Step(`^the release channel advertises latest "([^"]*)" with a tampered artifact$`, func(tag string) error {
		return setup(tag, []byte("TAMPERED\n"), true)
	})

	ctx.Step(`^it reports the current version "([^"]*)" and the available version "([^"]*)"$`, func(cur, latest string) error {
		if !strings.Contains(w.Out, "current: "+cur) || !strings.Contains(w.Out, "latest: "+latest) {
			return fmt.Errorf("check output = %q", w.Out)
		}
		return nil
	})

	ctx.Step(`^the current binary is unchanged$`, func() error {
		return assertUpdateContent(w, updateOriginBinary)
	})
	ctx.Step(`^the current binary is replaced with the new release$`, func() error {
		data, err := os.ReadFile(w.UpdateTarget)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(data), "NEW-BINARY-") || string(data) == updateOriginBinary {
			return fmt.Errorf("binary not replaced: %q", data)
		}
		return nil
	})
	ctx.Step(`^the command fails and the current binary is unchanged$`, func() error {
		if w.RunErr == nil {
			return errors.New("expected update to fail")
		}
		return assertUpdateContent(w, updateOriginBinary)
	})
	ctx.Step(`^the failure does not expose the downloaded artifact$`, func() error {
		if strings.Contains(w.Out, "TAMPERED") || strings.Contains(w.RunErr.Error(), "TAMPERED") {
			return fmt.Errorf("downloaded artifact leaked: %q / %v", w.Out, w.RunErr)
		}
		return nil
	})
}

func assertUpdateContent(w *fixture.World, want string) error {
	data, err := os.ReadFile(w.UpdateTarget)
	if err != nil {
		return err
	}
	if string(data) != want {
		return fmt.Errorf("binary content = %q, want %q", data, want)
	}
	return nil
}
