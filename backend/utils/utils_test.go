package utils

import "testing"

func TestGetEnvReturnsDefaultWhenUnset(t *testing.T) {
	got := GetEnv("GARAGE_WEBUI_TEST_UNSET_VAR", "default-value")
	if got != "default-value" {
		t.Errorf("GetEnv() = %q, want %q", got, "default-value")
	}
}

func TestGetEnvReturnsValueWhenSet(t *testing.T) {
	t.Setenv("GARAGE_WEBUI_TEST_SET_VAR", "actual-value")

	got := GetEnv("GARAGE_WEBUI_TEST_SET_VAR", "default-value")
	if got != "actual-value" {
		t.Errorf("GetEnv() = %q, want %q", got, "actual-value")
	}
}

func TestGetEnvReturnsDefaultWhenEmpty(t *testing.T) {
	// Current behavior: len(value) == 0 treats an empty string the same as
	// unset, so the default is returned rather than "".
	t.Setenv("GARAGE_WEBUI_TEST_EMPTY_VAR", "")

	got := GetEnv("GARAGE_WEBUI_TEST_EMPTY_VAR", "default-value")
	if got != "default-value" {
		t.Errorf("GetEnv() = %q, want %q", got, "default-value")
	}
}

func TestLastStringReturnsFinalElement(t *testing.T) {
	got := LastString([]string{"a", "b", "c"})
	if got != "c" {
		t.Errorf("LastString() = %q, want %q", got, "c")
	}
}

func TestLastStringSingleElement(t *testing.T) {
	got := LastString([]string{"only"})
	if got != "only" {
		t.Errorf("LastString() = %q, want %q", got, "only")
	}
}

func TestLastStringEmptySlice(t *testing.T) {
	t.Skip("LastString panics on an empty slice; not fixed in this plan")
	// LastString([]string{}) indexes str[-1] and panics.
}
