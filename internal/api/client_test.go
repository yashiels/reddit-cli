package api

import (
	"regexp"
	"testing"
)

// Reddit rejects non-UUID device_ids ("bad device_id"). Guard the format.
var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestDeviceIDIsUUIDv4(t *testing.T) {
	id := deviceID()
	if !uuidRE.MatchString(id) {
		t.Fatalf("device_id %q is not a v4 UUID — Reddit will reject it", id)
	}
}
