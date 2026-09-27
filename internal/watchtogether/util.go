package watchtogether

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

var errDenied = errors.New("denied")

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}
