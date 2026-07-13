// Package music2bb provides the presentation-independent engine for converting
// online playlists into Bilibili favorites.
//
// Returned structs and slices are caller-owned snapshots. The engine never
// reads a terminal, prints ANSI output, prompts, opens dialogs, or exits the
// process. Observer callbacks are serialized within each operation; separate
// concurrent operations may invoke their respective observers concurrently.
package music2bb
