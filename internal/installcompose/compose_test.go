package installcompose

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "docker-compose.yml")); err != nil {
		t.Fatalf("repo root %s missing docker-compose.yml: %v", root, err)
	}
	return root
}

func TestOnlyOneComposeFile(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"docker-compose.gpu.yml", "docker-compose.local.yml"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			t.Fatalf("%s must not exist; GPU and local overrides live in .env", name)
		}
	}
}

func TestComposeGPUIsProfiled(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, `profiles: ["cpu"]`) {
		t.Fatal("docker-compose.yml must define the cpu profile")
	}
	if !strings.Contains(text, `profiles: ["gpu"]`) {
		t.Fatal("docker-compose.yml must define the gpu profile")
	}
	if !strings.Contains(text, "gpus: all") {
		t.Fatal("docker-compose.yml must request gpus: all on the gpu profile")
	}
	if !strings.Contains(text, "NVIDIA_VISIBLE_DEVICES") {
		t.Fatal("docker-compose.yml must set NVIDIA_VISIBLE_DEVICES on the gpu profile")
	}
	if !strings.Contains(text, "NVIDIA_DRIVER_CAPABILITIES") {
		t.Fatal("docker-compose.yml must set NVIDIA_DRIVER_CAPABILITIES on the gpu profile")
	}
	gpuIdx := strings.Index(text, "viewdock-gpu:")
	gpusIdx := strings.Index(text, "gpus: all")
	if gpuIdx < 0 || gpusIdx < 0 || gpusIdx < gpuIdx {
		t.Fatal("gpus: all must belong to the viewdock-gpu service, not the cpu service")
	}
}

func TestEnvExampleGPU(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "VD_GPU=false") {
		t.Fatal(".env.example must default VD_GPU=false")
	}
	if !strings.Contains(text, "COMPOSE_PROFILES=cpu") {
		t.Fatal(".env.example must default COMPOSE_PROFILES=cpu")
	}
}

func TestInstallShDetectRules(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "detect_nvidia_docker()") {
		t.Fatal("install.sh must define detect_nvidia_docker")
	}
	const warn = "NVIDIA GPU detected but Docker GPU runtime is unavailable. ViewDock will use CPU transcoding."
	if !strings.Contains(text, warn) {
		t.Fatalf("install.sh must echo exactly: %s", warn)
	}
	if strings.Contains(text, "write_gpu_compose") {
		t.Fatal("install.sh must not write a GPU overlay file")
	}
	if strings.Contains(text, "COMPOSE_FILE=docker-compose.yml:docker-compose.gpu.yml") {
		t.Fatal("install.sh must not set COMPOSE_FILE to a GPU overlay")
	}
	if !strings.Contains(text, "VD_GPU=true") || !strings.Contains(text, "VD_GPU=false") {
		t.Fatal("install.sh must set VD_GPU true or false")
	}
	if !strings.Contains(text, "COMPOSE_PROFILES=gpu") || !strings.Contains(text, "COMPOSE_PROFILES=cpu") {
		t.Fatal("install.sh must sync COMPOSE_PROFILES from VD_GPU")
	}
	if !strings.Contains(text, "env_ensure_key()") {
		t.Fatal("install.sh must add missing .env keys without overwriting existing values")
	}
	if !strings.Contains(text, "Keeping existing") {
		t.Fatal("install.sh must keep existing .env and compose files when they are current")
	}
	if !strings.Contains(text, "migrate_legacy_install()") {
		t.Fatal("install.sh must migrate older overlay installs")
	}
	if !strings.Contains(text, "Never overwrites an existing VD_GPU") {
		t.Fatal("install.sh must not overwrite an existing VD_GPU")
	}

	start := strings.Index(text, "write_compose()")
	if start < 0 {
		t.Fatal("install.sh must define write_compose")
	}
	writeCompose := text[start:]
	if end := strings.Index(writeCompose, "\nensure_compose()"); end > 0 {
		writeCompose = writeCompose[:end]
	}
	if !strings.Contains(writeCompose, "gpus: all") {
		t.Fatal("write_compose() must emit gpus: all on the gpu profile")
	}
	if !strings.Contains(writeCompose, `profiles: ["cpu"]`) || !strings.Contains(writeCompose, `profiles: ["gpu"]`) {
		t.Fatal("write_compose() must emit cpu and gpu profiles")
	}
	if !strings.Contains(text, "ensure_compose()") {
		t.Fatal("install.sh must define ensure_compose")
	}
	if !strings.Contains(text, "compose_is_current()") {
		t.Fatal("install.sh must skip rewriting a current compose file")
	}
	if !strings.Contains(text, "clear_viewdock_runtime()") {
		t.Fatal("install.sh must stop leftover viewdock containers before recreate")
	}
	if !strings.Contains(text, "Compose up failed; clearing leftovers and retrying") {
		t.Fatal("install.sh must retry compose up after freeing the container name")
	}
	if !strings.Contains(text, "Network ${net} still has active endpoints") {
		t.Fatal("install.sh must detach leftover endpoints when the project network will not close")
	}
}
