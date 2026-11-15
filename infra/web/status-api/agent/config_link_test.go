package agent

import "testing"

func TestLinkURLDefault(t *testing.T) {
	for in, want := range map[string]string{
		"":                           DefaultLinkURL,
		"   ":                        DefaultLinkURL,
		"off":                        "",
		"OFF":                        "",
		"none":                       "",
		"0":                          "",
		"http://site/api/link/claim": "http://site/api/link/claim",
	} {
		if got := linkURL(in); got != want {
			t.Errorf("linkURL(%q) = %q, want %q", in, got, want)
		}
	}
	t.Setenv("AGENT_LINK_URL", "")
	c, err := FromEnv(nil)
	if err != nil || c.LinkURL != DefaultLinkURL {
		t.Errorf("unset: %q %v", c.LinkURL, err)
	}
	t.Setenv("AGENT_LINK_URL", "off")
	if c, _ := FromEnv(nil); c.LinkURL != "" {
		t.Errorf("off: %q", c.LinkURL)
	}
}
