package netpolicy

import "strings"

// Wildcards at or near a public suffix are refused.
//
// `*.example.com` is a company. `*.co.uk` is a country, `*.github.io` is
// everyone's GitHub Pages, and `*.s3.amazonaws.com` is every S3 bucket
// anyone has ever made — including one an attacker creates specifically
// because the grant exists. All three parsed and matched at any depth, and
// nothing in the prompt distinguished them from the first.
//
// This is a short list rather than the full Public Suffix List, and the
// difference is deliberate. The full list is ~10,000 entries that change
// weekly, which is a dependency and an update problem for a check whose job
// is to catch an obvious mistake. What is here covers the shapes that
// actually appear in a grant: multi-label country suffixes, and the shared
// hosting domains where "everything under this name" means "everybody".
//
// It is not a security boundary — someone determined can still grant
// `*.attacker.example`. It is a guard against a grant that is far wider
// than the person typing it believes.
var publicSuffixes = map[string]bool{
	// Country-code second-level domains: a wildcard here is a TLD.
	"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true, "me.uk": true,
	"com.au": true, "net.au": true, "org.au": true, "edu.au": true,
	"co.nz": true, "co.za": true, "co.jp": true, "or.jp": true, "ne.jp": true,
	"com.br": true, "com.cn": true, "com.mx": true, "co.in": true, "co.kr": true,
	"com.tr": true, "com.tw": true, "com.sg": true, "com.hk": true,

	// Shared hosting: one name, everybody's content.
	"github.io": true, "gitlab.io": true, "netlify.app": true, "vercel.app": true,
	"pages.dev": true, "workers.dev": true, "herokuapp.com": true,
	"azurewebsites.net": true, "cloudfront.net": true, "web.app": true,
	"firebaseapp.com": true, "readthedocs.io": true, "surge.sh": true,
	"s3.amazonaws.com": true, "blob.core.windows.net": true,
	"storage.googleapis.com": true, "r2.dev": true, "ngrok.io": true,
	"ngrok-free.app": true, "trycloudflare.com": true,
}

// isPublicSuffix reports whether a wildcard's parent is a name under which
// anyone can obtain a subdomain.
//
// A bare TLD counts: `*.com` has one label and is refused for the same
// reason, more obviously.
func isPublicSuffix(parent string) bool {
	parent = normalizeHost(parent)
	if !strings.Contains(parent, ".") {
		return true
	}
	return publicSuffixes[parent]
}

// suffixAdvice explains a refusal in terms of what the grant would have
// meant, since "public suffix" is jargon and the consequence is not.
func suffixAdvice(parent string) string {
	if !strings.Contains(normalizeHost(parent), ".") {
		return "that is a whole top-level domain"
	}
	return "anyone can register or create a name under " + parent
}
