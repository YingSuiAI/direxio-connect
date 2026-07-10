package gemini

import (
	"fmt"
	"io"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("CC_MOCK_GEMINI") == "1" {
		_, _ = io.ReadAll(os.Stdin)
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"result","status":"success","stats":{}}`)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
