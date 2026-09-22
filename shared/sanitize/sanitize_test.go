package sanitize

import (
	"testing"
)

func TestSanitizeText(t *testing.T) {
	input := "  <script>alert('xss')</script> Hello World! \x00 "
	expected := "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt; Hello World!"
	result := Text(input)
	if result != expected {
		t.Fatalf("expected '%s', got '%s'", expected, result)
	}
}

func TestSanitizeEmail(t *testing.T) {
	input := "  USER@Example.COM  "
	expected := "user@example.com"
	result := Email(input)
	if result != expected {
		t.Fatalf("expected '%s', got '%s'", expected, result)
	}
}
