package r2

import _ "embed"

// removalPNG is generated once, contains no user data, and is shared by owner
// deletion, administrator removal, and account purge.
//
//go:embed removal.png
var removalPNG []byte

func RemovalPNG() []byte { return append([]byte(nil), removalPNG...) }
