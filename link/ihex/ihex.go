// Package ihex writes Intel HEX files (spec/13-targets.md, §13.9).
package ihex

import (
	"fmt"
	"strings"
)

// Encode returns the Intel HEX records of data loaded at address addr:
// extended linear address records for the upper 16 bits, data records of
// 16 bytes, and the end-of-file record.
func Encode(addr uint32, data []byte) []byte {
	var b strings.Builder
	record := func(kind byte, off uint16, bytes []byte) {
		sum := byte(len(bytes)) + byte(off>>8) + byte(off) + kind
		fmt.Fprintf(&b, ":%02X%04X%02X", len(bytes), off, kind)
		for _, x := range bytes {
			fmt.Fprintf(&b, "%02X", x)
			sum += x
		}
		fmt.Fprintf(&b, "%02X\n", byte(-sum))
	}
	upper := -1
	for i := 0; i < len(data); {
		a := addr + uint32(i)
		if int(a>>16) != upper {
			upper = int(a >> 16)
			record(4, 0, []byte{byte(upper >> 8), byte(upper)})
		}
		// A record does not cross a 64 KiB boundary.
		n := min(16, len(data)-i, 0x10000-int(a&0xFFFF))
		record(0, uint16(a), data[i:i+n])
		i += n
	}
	record(1, 0, nil)
	return []byte(b.String())
}
