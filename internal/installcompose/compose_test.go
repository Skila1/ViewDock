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

func TestBaseComposeIsCPUSafe(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "gpus:") {
		t.Fatal("docker-compose.yml must not contain gpus:")
	}
	if strings.Contains(text, "NVIDIA_VISIBLE_DEVICES") {
		t.Fatal("docker-compose.yml must not contain NVIDIA_VISIBLE_DEVICES")
	}
	if strings.Contains(text, "NVIDIA_DRIVER_CAPABILITIES") {
		t.Fatal("docker-compose.yml must not contain NVIDIA_DRIVER_CAPABILITIES")
	}
}

func TestGPUOverlayHasNVIDIASettings(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docker-compose.gpu.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "gpus: all") {
		t.Fatal("docker-compose.gpu.yml must contain gpus: all")
	}
	if !strings.Contains(text, "NVIDIA_VISIBLE_DEVICES") {
		t.Fatal("docker-compose.gpu.yml must contain NVIDIA_VISIBLE_DEVICES")
	}
	if !strings.Contains(text, "NVIDIA_DRIVER_CAPABILITIES") {
		t.Fatal("docker-compose.gpu.yml must contain NVIDIA_DRIVER_CAPABILITIES")
	}
	if strings.Contains(text, "image:") {
		t.Fatal("docker-compose.gpu.yml must not set image (stay on :latest from the base file)")
	}
}

func TestLocalOverlayHasNoGPUs(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docker-compose.local.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "gpus:") {
		t.Fatal("docker-compose.local.yml must not contain gpus:")
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
	if !strings.Contains(text, "COMPOSE_FILE=docker-compose.yml:docker-compose.gpu.yml") {
		t.Fatal("install.sh must set COMPOSE_FILE to the GPU overlay when the runtime is present")
	}

	// write_compose heredoc must stay CPU-only; gpus: belongs only in write_gpu_compose.
	start := strings.Index(text, "write_compose()")
	end := strings.Index(text, "write_gpu_compose()")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("install.sh must define write_compose and write_gpu_compose in that order")
	}
	writeCompose := text[start:end]
	if strings.Contains(writeCompose, "gpus:") {
		t.Fatal("write_compose() must not emit gpus:")
	}
	if strings.Contains(writeCompose, "NVIDIA_VISIBLE_DEVICES") || strings.Contains(writeCompose, "NVIDIA_DRIVER_CAPABILITIES") {
		t.Fatal("write_compose() must not emit NVIDIA env vars")
	}
}
