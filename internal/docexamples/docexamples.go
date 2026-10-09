// Package docexamples reads the shell commands a documentation page shows and
// says what each one claims, so a page's examples can be held to the binary.
//
// A reference page is what an agent copies from, and its examples drift: a verb
// is renamed, a flag is dropped, an id takes another shape. This package is the
// pure half of the gate that stops it. It extracts the commands from a page's
// fenced shell blocks and reads the two things the page promises about each:
// the words of the kilasflow invocation (so a test can parse them against the
// command tree with no server) and the exit code the page says it ends with (so
// a runner can execute them against a booted one and compare).
//
// The convention it reads is small. A command that is expected to fail says so
// in a trailing comment, `# exit N`, where N is one digit; every other command
// is expected to exit 0.
package docexamples

import (
	"regexp"
	"strings"
)

// Example is one shell command shown in a fenced block.
type Example struct {
	// Line is the 1-based line of the page the command starts on.
	Line int
	// Block numbers the shell fences of the page from 0. A runner that seeds
	// state (a workflow to act on) seeds it once per block, because an earlier
	// block may end by deleting what a later one uses.
	Block int
	// Text is the command as written, minus its prompt and its trailing
	// comment, ready to hand to `sh -c`. A command split over several lines
	// keeps its backslash continuations.
	Text string
	// Words are the shell words of the kilasflow invocation within Text, quotes
	// removed and variables left unexpanded. A pipeline's other stages and any
	// redirection are not in it: `printf x | kilasflow auth login --token -`
	// yields the words of `kilasflow auth login --token -`.
	Words []string
	// Comment is the trailing comment, without its `#`.
	Comment string
	// ExpectedExit is N when Comment says `exit N`, and 0 otherwise.
	ExpectedExit int
	// ServerPath is true for an invocation that boots the server rather than
	// running a verb: a bare `kilasflow`, `kilasflow serve`, or one that starts
	// with a flag. Running one would not return.
	ServerPath bool
}

// exitAnnotation is how a comment says the command ends with a non-zero exit.
var exitAnnotation = regexp.MustCompile(`\bexit (\d)\b`)

// shellFences are the info strings that mark a fence as commands to run.
var shellFences = map[string]bool{"bash": true, "sh": true, "shell": true, "console": true}

// Extract returns every kilasflow command in the page's shell fences, in
// order. Output lines shown under a `$ `-prompted command, comment-only lines
// and commands that are not kilasflow (an export, a cd) are skipped.
func Extract(markdown string) []Example {
	var (
		examples []Example
		lines    = strings.Split(markdown, "\n")
		block    = -1
		inShell  bool
		inOther  bool
	)

	// pending is a command still being read: its first line, and the text so far.
	var (
		pendingLine int
		pending     []string
		output      bool
	)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		if example, ok := build(pendingLine, block, strings.Join(pending, "\n")); ok {
			examples = append(examples, example)
		}
		pending = nil
	}

	for index, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			switch {
			case inShell:
				flush()
				inShell, output = false, false
			case inOther:
				inOther = false
			default:
				info := strings.Fields(strings.TrimPrefix(trimmed, "```"))
				if len(info) > 0 && shellFences[strings.ToLower(info[0])] {
					inShell, output = true, false
					block++
				} else {
					inOther = true
				}
			}

			continue
		}
		if !inShell {
			continue
		}

		// A backslash at the end of a line continues the command on the next.
		if len(pending) > 0 && strings.HasSuffix(strings.TrimRight(pending[len(pending)-1], " \t"), `\`) {
			pending = append(pending, line)

			continue
		}
		flush()

		switch {
		case trimmed == "":
			output = false
		case strings.HasPrefix(trimmed, "$ "):
			output = true
			pendingLine, pending = index+1, []string{strings.TrimPrefix(trimmed, "$ ")}
		case output, strings.HasPrefix(trimmed, "#"):
			// What a prompted command printed, or a comment line.
		default:
			pendingLine, pending = index+1, []string{trimmed}
		}
	}
	flush()

	return examples
}

// build turns one command's text into an Example. ok is false when the text is
// not a kilasflow invocation.
func build(line, block int, text string) (Example, bool) {
	tokens, commentAt := tokenize(text)

	example := Example{Line: line, Block: block, Text: strings.TrimSpace(text)}
	if commentAt >= 0 {
		example.Text = strings.TrimSpace(text[:commentAt])
		example.Comment = strings.TrimSpace(strings.TrimPrefix(text[commentAt:], "#"))
	}
	if match := exitAnnotation.FindStringSubmatch(example.Comment); match != nil {
		example.ExpectedExit = int(match[1][0] - '0')
	}

	words, ok := kilasflowWords(tokens)
	if !ok {
		return Example{}, false
	}
	example.Words = words
	example.ServerPath = len(words) == 1 || words[1] == "serve" || strings.HasPrefix(words[1], "-")

	return example, true
}

// kilasflowWords is the words of the first pipeline stage that runs kilasflow.
func kilasflowWords(tokens []token) ([]string, bool) {
	var stage []string
	stages := make([][]string, 0, 2)
	skipTarget := false

	for _, current := range tokens {
		switch {
		case skipTarget:
			// The word after a redirection operator is a file, not an argument.
			skipTarget = false
		case current.op && isRedirect(current.text):
			skipTarget = true
		case current.op:
			stages = append(stages, stage)
			stage = nil
		default:
			stage = append(stage, current.text)
		}
	}
	stages = append(stages, stage)

	for _, words := range stages {
		// Leading NAME=value words are an environment, not the command.
		for len(words) > 0 && assignment.MatchString(words[0]) {
			words = words[1:]
		}
		if len(words) > 0 && words[0] == "kilasflow" {
			return words, true
		}
	}

	return nil, false
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func isRedirect(operator string) bool {
	return strings.HasPrefix(operator, "<") || strings.HasPrefix(operator, ">")
}

// token is a word or an operator of a command line.
type token struct {
	text string
	op   bool
}

// tokenize splits a command line the way a POSIX shell does, as far as a
// documentation example needs: single and double quotes, backslash escapes and
// continuations, `$(...)` as part of a word, the control operators, and a
// trailing comment. commentAt is the byte offset of the comment's `#`, or -1.
func tokenize(text string) (tokens []token, commentAt int) {
	var (
		word   strings.Builder
		inWord bool
		depth  int
	)
	commentAt = -1
	flush := func() {
		if inWord {
			tokens = append(tokens, token{text: word.String()})
		}
		word.Reset()
		inWord = false
	}

	runes := []rune(text)
	offsets := make([]int, len(runes)+1)
	for position, r := range runes {
		offsets[position+1] = offsets[position] + len(string(r))
	}

	for position := 0; position < len(runes); position++ {
		current := runes[position]

		if depth > 0 {
			// Inside $(...) everything belongs to the word until the parenthesis closes.
			word.WriteRune(current)
			switch current {
			case '(':
				depth++
			case ')':
				depth--
			}

			continue
		}

		switch {
		case current == '\\':
			if position+1 < len(runes) {
				position++
				if runes[position] == '\n' {
					continue
				}
				word.WriteRune(runes[position])
				inWord = true
			}
		case current == '\'':
			inWord = true
			for position++; position < len(runes) && runes[position] != '\''; position++ {
				word.WriteRune(runes[position])
			}
		case current == '"':
			inWord = true
			for position++; position < len(runes) && runes[position] != '"'; position++ {
				if runes[position] == '\\' && position+1 < len(runes) && strings.ContainsRune("\"\\$`", runes[position+1]) {
					position++
				}
				word.WriteRune(runes[position])
			}
		case current == '$' && position+1 < len(runes) && runes[position+1] == '(':
			inWord = true
			word.WriteString("$(")
			position++
			depth = 1
		case current == '#' && !inWord:
			commentAt = offsets[position]
			position = len(runes)
		case current == ' ' || current == '\t' || current == '\n' || current == '\r':
			flush()
		case strings.ContainsRune("|&;<>", current):
			// A bare number before a redirection is a file descriptor (`2>&1`).
			if (current == '<' || current == '>') && inWord && isDigits(word.String()) {
				word.Reset()
				inWord = false
			}
			flush()
			operator := string(current)
			switch {
			case position+1 < len(runes) && runes[position+1] == current && current != ';':
				operator += string(current)
				position++
			case (current == '<' || current == '>') && position+1 < len(runes) && runes[position+1] == '&':
				// `2>&1`: the descriptor after the ampersand is the target.
				operator += "&"
				position++
			}
			tokens = append(tokens, token{text: operator, op: true})
		default:
			word.WriteRune(current)
			inWord = true
		}
	}
	flush()

	return tokens, commentAt
}

func isDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}
