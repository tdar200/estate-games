// Package ludo is the rules engine for Ludo, the third game type of the
// Estate party server (after Estate and Texas hold'em). It knows nothing
// about rooms, phones or sockets: the server (internal/server/ludo.go) calls
// it under its own lock, exactly as it calls internal/poker/holdem.
//
// The default rules are the Pakistani/Indian style:
//
//   - a 6 brings a piece out of the yard onto its start square;
//   - a 6 or a capture gives another roll (and, by default, so does a piece
//     reaching home);
//   - three 6s in a row lose the turn, and the third 6 is not played (the
//     first two moves stand);
//   - star squares (every start square and the square eight steps after it)
//     are safe: nobody is captured there and colours may share them;
//   - a piece needs the exact number to reach home.
//
// House rules (Settings) switch some of these off or add the common
// variants: any roll brings a piece out (kids mode), no safe squares, no
// three-sixes rule, "capture before you may go home", blocks, 2 or 3 pieces
// each for a quicker game, and stop at the first winner instead of playing
// on for places.
//
// There is no hidden information in Ludo, so every viewer gets the same View
// (PublicState) apart from the "you" panel; bots get that View too. The dice
// come from a seeded ChaCha8 stream saved in the snapshot, so a restored game
// rolls exactly what it would have rolled.
//
// Files: board.go (geometry of the 4-arm and 6-arm boards), settings.go
// (house rules), game.go (lobby, turns, placing order, snapshot and restore),
// moves.go (legal moves, captures, blocks), view.go (the state clients and
// bots see), bots.go (Easy, Normal and Hard computer players).
package ludo
