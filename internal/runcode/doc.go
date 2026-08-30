// Package runcode compiles and executes user-supplied Go for the Code node.
//
// User code never runs in the kilasflow process. The intended path is
// GOOS=wasip1 compilation cached by source hash, executed under wazero.
//
// Open design question: compiling Go requires the full toolchain (~270MB),
// which cannot ship inside the distroless single binary. Resolve before
// Milestone 5 -- likely a separate compiler service.
//
// Milestone 5.
package runcode
