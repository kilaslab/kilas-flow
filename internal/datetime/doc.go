// Package datetime is the one place KilasFlow turns instants into text and
// text back into instants.
//
// It exists because two things need it and must not disagree: the Date & Time
// node, and the expression engine's own date roots. n8n's users write Luxon
// format tokens — `yyyy-MM-dd`, `cccc`, `HH:mm` — and Go writes layouts as a
// reference date, so something has to translate. Two translations would drift
// on the first token either one forgot, and the difference would show up as a
// timestamp that reads one way in a Set node and another way in a filename.
//
// Zones are always explicit. A function here that guessed the server's local
// zone would make a workflow's output depend on where it happened to be
// deployed, which is exactly the class of bug a scheduled workflow is worst at
// revealing: it is right for eleven months and wrong the week the clocks move.
package datetime
