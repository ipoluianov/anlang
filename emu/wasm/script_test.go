package wasm

import "testing"

// TestScriptChecks checks that failing assertions are reported, so that
// TestSpec cannot pass vacuously.
func TestScriptChecks(t *testing.T) {
	src := `
(module
  (func (export "one") (result i32) (i32.const 1))
  (func (export "nan") (result f32) (f32.const nan:0x600000))
  (func (export "trap") (unreachable)))
(assert_return (invoke "one") (i32.const 2))
(assert_return (invoke "nan") (f32.const nan:canonical))
(assert_trap (invoke "one") "unreachable")
(assert_malformed (module quote "(func (i32.const 1) drop)") "unknown operator")
(assert_invalid (module (func (result i32) (i32.const 1))) "type mismatch")
(assert_malformed (module quote "(func (i32.const))") "unexpected token")
(assert_invalid (module (func (result i32) (i64.const 1))) "type mismatch")
(assert_return (invoke "one") (i32.const 1))
(assert_return (invoke "nan") (f32.const nan:arithmetic))
(assert_trap (invoke "trap") "unreachable")
`
	s := NewScript()
	if err := s.Run(src); err != nil {
		t.Fatal(err)
	}
	if len(s.Failures) != 5 || s.Passed != 6 {
		t.Errorf("got %d passed and failures %q, want 6 passed and 5 failures", s.Passed, s.Failures)
	}
}
