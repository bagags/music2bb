# Classical catalogue references

Classical matching recognizes work references such as `BWV 1007`,
`Hob. XVI:52`, and `Wq 182/3`. Parsing is provided by the dependency-free
[`classical-catalogue-id-parser`](https://github.com/bagags/classical-catalogue-id-parser)
Go module; music2bb does not maintain a separate symbol registry or grammar.

The external module embeds the same revision-1 registry previously stored in
`internal/catalogue/registry.v1.json`: 130 sorted canonical symbols audited
from Wikipedia's complete Symbol column on 2026-07-15 at 16:20:03 UTC. Its
[`REGISTRY.md`](https://github.com/bagags/classical-catalogue-id-parser/blob/main/REGISTRY.md)
documents the full source provenance, version policy, strict identifier
grammar, and normalization rules.

## Matcher behavior

Only the `classical` profile consults the catalogue parser. A shared reference
sets the candidate's title component to 100 before the existing six configured
weights are applied. A different reference does not reduce or otherwise change
the ordinary similarity score. The `standard` profile, query phases, custom
weights, `KeywordScore` compatibility alias, total thresholds, and ambiguity
rules are unchanged.

## Extraction compatibility

The parser implementation, registry, and parser tests moved unchanged into the
standalone module. The required extraction changes are limited to the import
path and registry/documentation ownership; parsing, normalization, shared-work
comparison, and music2bb scoring behavior are unchanged.
