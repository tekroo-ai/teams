package promptpolicy

import _ "embed"

const Version = "tekroo.teams.instructions/1.0.0"

// Universal is the versioned Teams instruction layer. Any change to its text
// changes the assembled prompt digest and requires qualification before use.
//go:embed tekroo.md
var Universal string
