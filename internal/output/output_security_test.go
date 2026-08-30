package output

import (
	"bytes"
	"testing"
)

func TestWriteJSONEscapesHTMLAndEmitsOneObject(t *testing.T) {
	var out bytes.Buffer
	if err := WriteJSON(&out, Success("test", false, map[string]string{"value": "<script>alert(1)</script>"}, nil)); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Bytes(), []byte("<script>")) || !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
		t.Fatalf("unsafe JSON output %q", out.String())
	}
}
