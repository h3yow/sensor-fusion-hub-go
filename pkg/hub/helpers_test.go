package hub_test

import (
	"fmt"
	"strings"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
)

func fmtResults(fs []sensor.FusedResult) string {
	var b strings.Builder
	for _, f := range fs {
		fmt.Fprintf(&b, "%s:%s:%s:%.6f:%d\n", f.Kind, f.Status, f.Reason, f.M.Temperature, f.CalEpoch)
	}
	return b.String()
}
