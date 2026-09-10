package findings

// All returns every rule in the order they are documented. Rules are added
// here as they are implemented.
func All() []Rule {
	return []Rule{
		readOnlyAgentRule{},
		requestedModelRule{},
		cacheRebuildRule{},
		toolOutputRule{},
		retryLoopRule{},
		thinkingRelayRule{},
	}
}
