# Development Instructions

## Working process

Make the smallest sufficient change. Design non-trivial logic so it can be tested in isolation.

Before making changes:

1. Read this file, `README.md`, and `CONTRIBUTING.md` if present. Read any instructions that apply to the target directory.
2. Read the relevant sections of [the technical specification](docs/technical-specification-v1.md) and the documents they reference when needed for the task.
3. Inspect the existing code, tests, build commands, and Git status. Preserve user changes.
4. Identify the required behavior, affected layers, and test cases before implementation.

Keep product requirements and implementation decisions in the specification and related documentation, not in this file. Do not duplicate them here.

Ask only when a missing decision blocks the current task; continue independent work. Document reasonable assumptions. Do not treat examples or open questions as approved requirements.

## Language and communication

- Use simple, clear level B1 English.
- Use English for all project content: code, identifiers, comments, documentation, tests, test names, fixtures written for this project, logs, error messages, configuration descriptions, commit messages, and pull requests.
- Communicate with the user in Russian unless requested otherwise. Be brief, direct, and factual. Explain unnecessary complexity or incorrect assumptions without motivational filler.
- Keep code, comments, and project reports neutral and professional.
- Write new documentation and documentation updates in English. Preserve the meaning of existing requirements when translating them.

## Specifications

Write specifications in simple English at B1 level. Use short sentences and explain technical terms when needed. Describe what will change and why without repeating the same rule in several sections.

Use the following section order. Required sections must be present. Add optional sections only when they have useful content; do not add empty placeholders.

1. **Summary / Overview** (required): briefly state the change and its purpose.
2. **Context / Background** (required): describe the current system and relevant constraints.
3. **Problem Statement** (required): explain the specific failure or missing behavior.
4. **Goals** (required): state the outcomes we need.
5. **Non-Goals** (optional): define work outside the scope.
6. **Requirements** (optional): list distinct functional or quality requirements when needed.
7. **Proposed Solution / Design** (required): explain the solution and how the parts work together.
8. **Alternatives Considered** (optional): record real alternatives and reasons for rejecting them.
9. **Data Model / API / Interfaces** (required): describe state and contract changes; state briefly when no changes are needed.
10. **Failure Modes / Edge Cases** (required when applicable): describe failures and expected behavior.
11. **Security / Privacy** (optional): cover relevant security or privacy changes.
12. **Observability** (optional): define useful logs, metrics, traces, or alerts.
13. **Migration / Rollout Plan** (optional): explain implementation stages, configuration migration, and release steps.
14. **Rollback Plan** (optional): explain how to return to the earlier version.
15. **Testing / Validation** (required): define checks and acceptance criteria.
16. **Risks / Trade-offs** (optional, when present): explain limits and accepted compromises.
17. **Open Questions** (optional): list unresolved decisions, separate from agreed behavior.

When phases are useful, put them in the rollout plan: first a short list, then details and expected results. Link to design and tests instead of repeating them. Put sources and earlier requirement replacements after the main sections when needed.

- Use concrete examples to explain important rules and boundaries.
- Keep agreed constraints, acceptance criteria, and open decisions explicit. Distinguish proposals from confirmed requirements.
- Include technical details only when they prevent ambiguity or an implementation error. Link to existing documentation instead of repeating it.
- Avoid repeated requirements, exhaustive scenario lists, speculative abstractions, and internal implementation inventories. Add a separate detailed note only when a specific issue needs it.
- Scale length to the task. The main specification should be as easy to understand as a concise explanation in conversation; completeness does not require maximum length.

## Clean architecture

Keep dependencies pointing inward: infrastructure and transport depend on application contracts; application depends on domain. Inner layers must not depend on outer layers.

- Domain contains entities, value types, and business rules. Keep it independent of I/O and application orchestration.
- Application coordinates use cases through consumer-owned interfaces. It must not depend on concrete infrastructure or transport implementations.
- Infrastructure implements external integrations and persistence behind those interfaces. Keep third-party types and formats inside adapters.
- Transport handles protocol concerns and delegates use cases and business validation to application code.
- Construct concrete dependencies at the entry point. Keep business logic out of startup code.
- Pass explicit settings into services. Keep configuration loading and environment access outside business logic.

Keep domain and application independent of SDKs, transport frameworks, database clients, and observability integrations. Follow the existing project layout; do not create layers or directories only to match a diagram.

Define interfaces near the application code that consumes them. Use constructor injection. Add an interface for a real boundary, not for every struct. Do not add generic repository frameworks, service locators, dependency injection containers, empty layers, or packages for hypothetical features.

Separate calculations from I/O. Keep business rules and validation directly testable. Inject time and external effects where needed for deterministic behavior. Avoid mutable global state.

## Go coding style

- Use standard Go formatting with `gofmt`. Keep a blank line between top-level type, function, and method declarations. Within functions, separate logical blocks with blank lines and keep related statements together; in tests, visually separate setup, action, and assertions. `gofmt` does not add this logical spacing for you. Avoid dense one-line control flow or multiple statements on one line. Use clear names and small, focused functions.
- Make the smallest sufficient change. Preserve the existing style and public interfaces unless the task requires a change.
- Prefer the standard library. Add dependencies only for a concrete requirement.
- Prefer unexported types and functions unless another package needs them.
- Pass `context.Context` through operations that can block. Give every worker a clear owner, cancellation path, and shutdown wait.
- Handle errors explicitly. Add useful context while preserving error identity with wrapping; use `errors.Is` or `errors.As` where appropriate. Do not use panic for expected failures.
- Keep shared data safe under concurrent access. Do not expose internal mutable state through returned maps, slices, or pointers.
- Keep comments focused on non-obvious reasons or constraints. Avoid comments that repeat the code.
- Do not refactor unrelated code, add speculative abstractions, or change production behavior just to satisfy a test.

## Tests: behavior, not call choreography

Unit tests are required for every non-trivial logic change. Bug fixes need a regression test that demonstrates the failure. Test observable inputs, outputs, state changes, invariants, and errors.

- Keep tests simple and easy to read. Repeated code in tests is acceptable; prefer clear, self-contained scenarios over abstractions added only to remove duplication. Keep helpers small and explicit, and avoid generic test frameworks or reflection that hide setup, actions, or expected results.
- Use table-driven tests when cases share the same setup, action, and assertion structure. Define named test cases first, then use one shared execution loop with `t.Run`. Each subtest must create its own state from its case data.
- For non-table-driven tests, write one test function per test case, with independent setup and one scenario. Do not chain unrelated cases by changing configuration and repeating actions in one test. Multiple assertions are appropriate when they verify the same scenario.
- Always use the current test's `t.Context()` as the root context, including inside subtests. Do not use `context.Background()` or `context.TODO()` in tests. Derive explicit cancellation and deadlines from `t.Context()`, and pass the test context to blocking operations and HTTP requests so their lifetime stays tied to the test.
- Exercise real logic. Prefer small stateful fakes at I/O boundaries over mocks of internal methods.
- Do not treat an assertion that a method was called as proof that a feature works. Avoid tests coupled to private helper names, incidental call order, or the current decomposition of a use case.
- Call counts are valid only when they are an explicit part of the required observable behavior. Also assert returned data, resulting state, or the expected failure.
- Cover normal cases, boundary values, empty input, malformed input, missing optional values, dependency failures, cancellation, and deadlines as relevant.
- Keep expected values explicit; do not recreate the production algorithm to compute the expected result.
- Use controlled clocks, timers, and synchronization for time and concurrency tests. Do not rely on arbitrary sleeps, random scheduling, external services, or wall-clock timing for correctness.
- Keep unit tests beside the code in `*_test.go`. Use the standard `testing` package as the test runner and `testify/assert` or `testify/require` for assertions. Use `require` for prerequisites that must stop the test on failure, and `assert` for independent result checks. Prefer clear assertions over manual `if` blocks with `t.Fatal` or `t.Errorf`. Do not add a mocking framework by default.
- Do not test trivial getters, setters, or pure data containers without meaningful behavior. Coverage numbers help locate gaps; they are not a substitute for assertions and are not a reason to add empty tests.

Keep unit tests isolated from external services and credentials. Use integration tests to verify real component boundaries and serialization paths; they do not replace unit tests for business logic. Derive feature-specific scenarios from the specification instead of maintaining a second list here.

## Verification and delivery

For Go changes, format changed Go files with `gofmt`, run focused tests while developing, then run the relevant project checks. Use the Makefile targets when present; otherwise use these commands once a Go module exists:

```sh
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Run the configured linter when available. Check coverage when it helps identify missing behavior; do not invent a percentage target. For documentation-only changes, verify the content, links, and whitespace; Go tests are not required.

Before delivery, inspect the diff for unrelated changes and secrets. Do not delete files, run destructive Git commands, or revert user changes without an explicit request. Never commit credentials, tokens, or keys; report discovered secrets without reproducing their values.

Finish with a short, verifiable report: what changed, which files changed, checks and results, and remaining risks or open decisions. State exactly which checks could not run and why. Do not claim the service or tests are complete when only scaffolding or documentation exists.
