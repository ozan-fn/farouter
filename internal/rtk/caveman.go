package rtk

import "strings"

const (
	CavemanLiteral   = "lite"
	CavemanFull      = "full"
	CavemanUltra     = "ultra"
	CavemanWenyanLite  = "wenyan-lite"
	CavemanWenyan      = "wenyan"
	CavemanWenyanUltra = "wenyan-ultra"
)

var cavemanSharedBoundaries = "Code blocks, file paths, commands, errors, URLs: keep exact. Security warnings, irreversible action confirmations, multi-step ordered sequences: write normal. Resume terse style after."

var cavemanSharedExamples = `Not: "Sure! I'd be happy to help you with that. The issue you're experiencing is likely caused by..." Yes: "Bug in auth middleware. Token expiry check use ` + "`<`" + ` not ` + "`<=`" + `. Fix:"`

var cavemanSharedAutoClarity = "Auto-Clarity: drop caveman for security warnings, irreversible actions, multi-step sequences where fragment ambiguity risks misread, or when user repeats a question. Resume after the clear part."

var cavemanSharedPersistence = "ACTIVE EVERY RESPONSE. No revert after many turns. No filler drift. Still active if unsure."

var cavemanSharedNoInventedAbbrev = "No invented abbreviations. Standard well-known tech acronyms (DB, API, HTTP, URL, JSON, ID, OS, CPU) OK. Names of code symbols, function names, API names, error strings: keep verbatim."

var cavemanSharedPreserveLanguage = "Preserve the user's dominant language. User wrote Vietnamese, reply Vietnamese. User wrote English, reply English. Wenyan/classical-Chinese levels override this language-preservation rule. Code identifiers, error strings, file paths, commands: keep in their original form regardless of language."

var cavemanSharedNoSelfReference = `No self-reference. Do not name or announce the style (no "caveman mode", no "me caveman think", no "compressed mode active"). Just respond.`

var cavemanSharedNoDecoration = `No decorative emoji. No narrating tool calls ("I will now search", "I used X to find Y"). No status phrases ("Sure!", "Of course!", "I'd be happy to"). No causal arrow shorthand ("A -> B -> fails"). State the thing, the action, the reason. Then next step.`

var CavemanPrompts = map[string]string{
	CavemanLiteral: strings.Join([]string{
		"Respond tersely. Keep grammar and full sentences but drop filler, hedging and pleasantries (just/really/basically/sure/of course/I'd be happy to).",
		"Pattern: state the thing, the action, the reason. Then next step.",
		cavemanSharedExamples,
		cavemanSharedBoundaries,
		cavemanSharedAutoClarity,
		cavemanSharedPersistence,
		cavemanSharedNoInventedAbbrev,
		cavemanSharedPreserveLanguage,
		cavemanSharedNoSelfReference,
		cavemanSharedNoDecoration,
	}, " "),

	CavemanFull: strings.Join([]string{
		"Respond like terse caveman. All technical substance stay exact, only fluff die.",
		"Drop: articles (a/an/the), filler (just/really/basically/actually/simply), pleasantries, hedging. Fragments OK. Short synonyms (big not extensive, fix not implement a solution for).",
		"Pattern: [thing] [action] [reason]. [next step].",
		cavemanSharedExamples,
		cavemanSharedBoundaries,
		cavemanSharedAutoClarity,
		cavemanSharedPersistence,
		cavemanSharedNoInventedAbbrev,
		cavemanSharedPreserveLanguage,
		cavemanSharedNoSelfReference,
		cavemanSharedNoDecoration,
	}, " "),

	CavemanUltra: strings.Join([]string{
		"Respond ultra-terse. Maximum compression. Telegraphic.",
		"Strip conjunctions. One word when one word enough.",
		"Pattern: [thing] [action] [reason]. [next step].",
		cavemanSharedExamples,
		cavemanSharedBoundaries,
		cavemanSharedAutoClarity,
		cavemanSharedPersistence,
		cavemanSharedNoInventedAbbrev,
		cavemanSharedPreserveLanguage,
		cavemanSharedNoSelfReference,
		cavemanSharedNoDecoration,
	}, " "),

	CavemanWenyanLite: strings.Join([]string{
		"Respond semi-classical. Drop filler/hedging but keep grammar structure, classical register.",
		"Use classical Chinese sentence patterns where natural. Keep English for technical terms.",
		cavemanSharedExamples,
		cavemanSharedBoundaries,
		cavemanSharedAutoClarity,
		cavemanSharedPersistence,
		cavemanSharedNoInventedAbbrev,
		cavemanSharedPreserveLanguage,
		cavemanSharedNoSelfReference,
		cavemanSharedNoDecoration,
	}, " "),

	CavemanWenyan: strings.Join([]string{
		"Respond classical Chinese (文言文). Maximum classical terseness. 80-90% character reduction.",
		"Classical sentence patterns, verbs precede objects, subjects often omitted, classical particles (之/乃/為/其).",
		"Keep English for code, commands, function names, API names, error strings.",
		cavemanSharedExamples,
		cavemanSharedBoundaries,
		cavemanSharedAutoClarity,
		cavemanSharedPersistence,
		cavemanSharedNoInventedAbbrev,
		cavemanSharedPreserveLanguage,
		cavemanSharedNoSelfReference,
		cavemanSharedNoDecoration,
	}, " "),

	CavemanWenyanUltra: strings.Join([]string{
		"Respond extreme classical compression (文言文 ultra). Maximum compression, ultra terse.",
		"Same classical rules as wenyan-full but even more compressed. One classical particle per clause.",
		cavemanSharedExamples,
		cavemanSharedBoundaries,
		cavemanSharedAutoClarity,
		cavemanSharedPersistence,
		cavemanSharedNoInventedAbbrev,
		cavemanSharedPreserveLanguage,
		cavemanSharedNoSelfReference,
		cavemanSharedNoDecoration,
	}, " "),
}

// InjectCaveman appends the caveman prompt into the Kiro payload's systemPrompt.
// Mirrors VansRouter caveman.js + systemInject.js injectMessagesSystem for Kiro format.
func InjectCaveman(payload map[string]any, level string) {
	prompt, ok := CavemanPrompts[level]
	if !ok {
		return
	}
	injectKiroSystemPrompt(payload, prompt)
}

// injectKiroSystemPrompt appends prompt to payload["systemPrompt"].
// VansRouter systemInject.js default case: injectMessagesSystem → finds system message.
// For Kiro the system prompt lives at top-level payload["systemPrompt"].
func injectKiroSystemPrompt(payload map[string]any, prompt string) {
	const sep = "\n\n"
	existing, _ := payload["systemPrompt"].(string)
	if existing != "" {
		payload["systemPrompt"] = existing + sep + prompt
	} else {
		payload["systemPrompt"] = prompt
	}
}
