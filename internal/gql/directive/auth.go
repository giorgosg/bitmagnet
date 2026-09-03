package directive

type (
	AuthDirective struct {
		Object string
		Action string
	}

	AuthDirectives map[AuthDirective]struct{}
)

func ExtractAuthDirectives(directives SchemaDirectives) AuthDirectives {
	result := make(AuthDirectives)

	// SchemaDirectives nests type -> field -> directives, so name each level for
	// what it holds. Shadowing one identifier three deep made the innermost value
	// -- the only one actually read -- indistinguishable from the map it came from.
	for _, fields := range directives {
		for _, fieldDirectives := range fields {
			for _, args := range fieldDirectives {
				result[AuthDirective{
					Action: args["action"],
					Object: args["object"],
				}] = struct{}{}
			}
		}
	}

	return result
}
