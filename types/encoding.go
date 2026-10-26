package types

// The encodings of package an/encoding (spec/10-compile-time.md §10.7).

// cp866High are the characters of the bytes 0x80 to 0xFF of code page
// 866, the Cyrillic code page of DOS.
var cp866High = []rune("" +
	"АБВГДЕЖЗИЙКЛМНОП" + "РСТУФХЦЧШЩЪЫЬЭЮЯ" +
	"абвгдежзийклмноп" + "░▒▓│┤╡╢╖╕╣║╗╝╜╛┐" +
	"└┴┬├─┼╞╟╚╔╩╦╠═╬╧" + "╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀" +
	"рстуфхцчшщъыьэюя" + "ЁёЄєЇїЎў°∙·√№¤■ ")

var cp866 = func() map[rune]byte {
	m := map[rune]byte{}
	for i, r := range cp866High {
		m[r] = byte(0x80 + i)
	}
	return m
}()

// encodeCP866 converts text to code page 866. It returns the first
// character the code page cannot represent, if any
// (ct.encoding.unmappable).
func encodeCP866(s string) ([]byte, string) {
	var b []byte
	for _, r := range s {
		switch {
		case r < 0x80:
			b = append(b, byte(r))
		case cp866[r] != 0:
			b = append(b, cp866[r])
		default:
			return nil, string(r)
		}
	}
	return b, ""
}
