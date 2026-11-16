// digestpreview renders the weekly email for made-up numbers into digest.html and digest.txt in the current folder, to look at the design
// without a database or a mail service.  Usage: digestpreview [username [first|rough]]
//
//	(none)  a good week      first  a short first week, nothing to compare with      rough  a worse week than the one before
package main

import (
	"fmt"
	"os"
	"time"

	"s4ndmod26/status-api/digest"
)

func main() {
	name, variant := "S4NDM4NN", ""
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	if len(os.Args) > 2 {
		variant = os.Args[2]
	}
	d := digest.Sample(name, variant, time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))
	d.BaseURL, d.UnsubURL = "https://s4ndmod.com", "https://s4ndmod.com/account/unsubscribe.html?t=example"
	subject, html, text := digest.Render(d)
	os.WriteFile("digest.html", []byte(html), 0o644)
	os.WriteFile("digest.txt", []byte(text), 0o644)
	fmt.Println("subject:", subject)
}
