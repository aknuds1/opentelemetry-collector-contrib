// Code generated by mdatagen. DO NOT EDIT.

package sigv4authextension

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"), goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"))
}
