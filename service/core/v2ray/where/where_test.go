package where

import "testing"

func TestVariantFromVersionOutput(t *testing.T) {
	cases := map[string]Variant{
		// official xray reports "Xray <ver> (...)"
		"Xray": XrayCore,
		"xray": XrayCore,
		"XRAY": XrayCore,
		// merged core reports "V2RAYA_CORE <ver> (xray-core) ..."
		"V2RAYA_CORE": V2rayaCore,
		"v2raya_core": V2rayaCore,
		// anything unrecognized defaults to the merged core
		"V2Ray": V2rayaCore,
		"":      V2rayaCore,
	}
	for in, want := range cases {
		if got := variantFromVersionOutput(in); got != want {
			t.Errorf("variantFromVersionOutput(%q) = %q, want %q", in, got, want)
		}
	}
}
