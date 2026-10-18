// Package webapp incorpora nel binario i file statici della Mini App
// Telegram (index.html, css/, js/), che in Python erano serviti da Tornado
// con StaticFileHandler a partire dalla directory webapp/.
//
// I file web non vanno modificati qui: questo package li espone soltanto
// come fs.FS.
package webapp

import (
	"embed"
	"io/fs"
)

//go:embed index.html css js
var files embed.FS

// FS restituisce il file system della Mini App, con radice nella directory
// webapp/ (contiene index.html, css/style.css e js/app.js).
func FS() fs.FS {
	return files
}
