// Package pycompat raccoglie gli helper che replicano il comportamento della
// libreria standard di Python 3.13 usato da api/auth.py, api/handlers.py,
// health_handler.py e broadcast.py, così che le risposte HTTP, i messaggi
// d'errore e i log della versione Go restino identici byte per byte:
//
//   - json.dumps (anche con indent) e json.loads, con dict ordinati (Object),
//     interi di precisione arbitraria e repr() dei float;
//   - str() e repr() dei valori decodificati da JSON, repr() di bytes e set;
//   - int() in base 10 e str.strip() / str.isspace();
//   - urllib.parse.parse_qs / unquote (UTF-8 con errors="replace");
//   - bytes.decode("utf-8") stretto (messaggi di UnicodeDecodeError) e con
//     errors="replace" (sostituzione delle "maximal subparts");
//   - pathlib: Path.resolve() (os.path.realpath), exists(), is_file(),
//     read_text() e i messaggi str(OSError) ("[Errno 2] No such file ...");
//   - datetime.isoformat() e datetime.strptime(s, "%Y-%m-%d").
//
// Le eccezioni Python sono rappresentate da *Error, il cui Error() è str(e);
// IsInstance replica isinstance() sulla gerarchia delle classi usate.
package pycompat
