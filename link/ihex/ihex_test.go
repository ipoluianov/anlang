package ihex

import "testing"

func TestEncode(t *testing.T) {
	got := string(Encode(0x0800FFF8, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}))
	want := ":020000040800F2\n" +
		":08FFF8000102030405060708DD\n" +
		":020000040801F1\n" +
		":02000000090AEB\n" +
		":00000001FF\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
