package findings

// All returns every rule in the order they are documented. Rules are added
// here as they are implemented. The order only matters for ties: Run sorts
// by saving, then keeps registration order.
func All() []Rule {
	return []Rule{
		// Sub-agent model choice.
		readOnlyAgentRule{},
		requestedModelRule{},
		effortAgentsRule{},
		// Main-session model choice.
		mainSessionRule{},
		// Context size.
		toolOutputRule{},
		longContextRule{},
		repeatedCallsRule{},
		compactionRule{},
		// Prompt cache lifetime.
		cacheRebuildRule{},
		cache1hRule{},
		// Thinking.
		thinkingRelayRule{},
		// Pointers rather than savings.
		retryLoopRule{},
		breakEvenRule{},
	}
}
