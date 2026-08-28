package inspector

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/viewdock/viewdock/internal/capability"
)

func TestGPUNullable(t *testing.T) {
	d := Build(Input{ID: "s1", Reasons: []string{"DIRECT_PLAY"}, Client: capability.Profile{}})
	b, _ := json.Marshal(d)
	if !strings.Contains(string(b), `"gpu":null`) {
		t.Fatalf("gpu should be null: %s", b)
	}
	d = Build(Input{ID: "s1", GPUAvail: true, VAAPI: true, HWAccel: "vaapi", Reasons: []string{}})
	if d.GPU == nil || !d.GPU.VAAPI {
		t.Fatal("gpu present")
	}
}
