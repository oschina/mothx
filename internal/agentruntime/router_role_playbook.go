package agentruntime

// rolePlaybook returns the bounded worker guidance for one delegated Router
// role. The role is a per-delegation intent hint, not an authorization
// boundary: an external ACP Agent runs its own tools, so these lines shape the
// prompt but cannot confine filesystem or process access.
//
// The guidance is owned here for the same reason the task contract is: the ACP
// worker bridge and any future adapter must render the same role semantics
// instead of each inventing its own checklist. The text is deliberately short
// and role-specific so it stays inside the bounded worker prompt.
func rolePlaybook(role SubagentRole) string {
	switch role {
	case SubagentRoleInvestigate:
		return `- Find the smallest set of files and symbols that answers the objective before widening the search.
- Report paths, line numbers, and the call or data flow you actually observed.
- Separate confirmed facts from hypotheses; list unresolved questions instead of guessing.
- Do not propose or perform edits; this role returns findings.`
	case SubagentRoleImplement:
		return `- Change only what the objective and write scope require; match the surrounding style and public APIs.
- Do not refactor unrelated code, rename exported symbols, or add speculative abstractions.
- Report every changed path, why it changed, and the exact verification you ran.
- If the requested change cannot fit the declared scope, stop and report the boundary instead of widening it.`
	case SubagentRoleTest:
		return `- Establish a baseline or reproduction before changing anything; record the command and its result.
- Run the narrowest check that exercises the behavior, then any adjacent suite the change can affect.
- Report the command, exit code, relevant output, and coverage gaps; never report a pass you did not observe.
- Treat flaky, skipped, or environment-blocked checks as unresolved evidence, not success.`
	case SubagentRoleReview:
		return `- Review against the objective and the repository's conventions, not personal preference.
- Rank findings by severity; a blocking finding needs concrete evidence (path, line, snippet) and stated impact.
- Keep style nits separate from correctness, security, and missing-test risks.
- Do not claim tests, builds, or runtime behavior passed unless you observed the result.`
	case SubagentRoleIntegrate:
		return `- Integrate only the declared change set or selected artifacts; never invent replacement files.
- Resolve conflicts in favor of the recorded base and preserve rejected changes for review.
- Report the applied paths, conflict decisions, and post-integration verification.
- Do not silently copy a mutable worker workspace into the base workspace.`
	default:
		return ""
	}
}
