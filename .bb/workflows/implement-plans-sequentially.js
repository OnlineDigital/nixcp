export const meta = {
  name: "implement-plans-sequentially",
  description: "Implement, test, commit, and push every numbered NixCP stage strictly in documentation order",
  phases: [
    { title: "Implement", detail: "Implement stages sequentially, committing and pushing after each one" },
  ],
};

const stages = [
  {
    order: 1,
    plan: "plans/01-product-scope-and-command-contract.md",
    title: "Product scope and command contract",
  },
  {
    order: 2,
    plan: "plans/02-go-architecture-and-cli-foundation.md",
    title: "Go architecture and CLI foundation",
  },
  {
    order: 3,
    plan: "plans/03-state-model-and-yaml-schemas.md",
    title: "State model and YAML schemas",
  },
  {
    order: 4,
    plan: "plans/04-installation-and-nixos-module-generation.md",
    title: "Installation and NixOS module generation",
  },
  {
    order: 5,
    plan: "plans/05-transactions-locking-and-rebuilds.md",
    title: "Transactions, locking, and rebuilds",
  },
  {
    order: 6,
    plan: "plans/06-system-services.md",
    title: "System services",
  },
  {
    order: 7,
    plan: "plans/07-php-versions-extensions-and-shells.md",
    title: "PHP versions, extensions, and shells",
  },
  {
    order: 8,
    plan: "plans/08-sites-nginx-and-databases.md",
    title: "Sites, Nginx, and databases",
  },
  {
    order: 9,
    plan: "plans/09-security-permissions-and-validation.md",
    title: "Security, permissions, and validation",
  },
  {
    order: 10,
    plan: "plans/10-testing-milestones-and-release.md",
    title: "Testing milestones and release",
  },
];

phase("Implement");
const completed = [];
for (let index = 0; index < stages.length; index += 1) {
  const stage = stages[index];
  log(`Starting stage ${stage.order}: ${stage.plan}`);

  const result = await agent(
    `You own NixCP implementation stage ${stage.order} of ${stages.length}: ${stage.title}.
Plan document: ${stage.plan}

Work directly in the current repository and current branch. Earlier stages have been implemented, committed, and pushed by preceding workers in this same strictly sequential workflow. Do not use or create a worktree.

Required procedure:
1. Read plans/overview.md, the complete ${stage.plan}, and any earlier plan documents or implementation files needed to understand the existing architecture.
2. Inspect git status and recent history before editing. The tree must be clean. Never reset, revert, amend, squash, or rewrite prior workers' commits.
3. Implement every applicable requirement in this stage. Preserve all product constraints and permanent exclusions. Do not replace real behavior with TODOs, placeholders, mocks in production code, or knowingly incomplete stubs.
4. Integrate with all prior stages and add/update focused tests. Keep deterministic human and --json behavior where relevant.
5. Run formatting, static checks, unit/integration tests, and build checks appropriate for all code changed so far. If a host-only NixOS operation cannot safely run, test pure generation/validation paths and clearly record the limitation; do not mutate the host OS.
6. Re-read the stage acceptance criteria and inspect the final diff. Fix any defect you find. Ensure no credentials, generated junk, or unrelated changes are included.
7. Commit all stage changes in exactly one new commit with a clear conventional message containing the stage number. Do not amend an existing commit.
8. Push that commit to origin on the current branch. Verify the pushed remote branch resolves to the same commit as local HEAD. A local-only commit is failure.

Do not stop after analysis or merely report recommendations: perform the implementation, tests, commit, and push. If blocked, do not fabricate success and do not create a partial commit; explain the precise blockers and leave the worktree clean. Otherwise end your ordinary text response with these exact lines:
STATUS: completed
COMMIT: <full 40-character SHA>
PUSHED: yes
TESTS: <semicolon-separated commands run>
SUMMARY: <one concise sentence>`,
    {
      provider: "pi",
      model: "omniroute/xk/openai/gpt-5.3-codex-spark",
      reasoningLevel: "none",
      label: `Stage ${stage.order}: ${stage.title}`,
      phase: "Implement",
    },
  );

  if (
    typeof result !== "string" ||
    !result.includes("STATUS: completed") ||
    !result.includes("PUSHED: yes") ||
    !/COMMIT: [0-9a-f]{40}/.test(result)
  ) {
    throw new Error(
      `Stage ${stage.order} did not report a completed, pushed 40-character commit. Result: ${String(result)}`,
    );
  }

  completed.push({ order: stage.order, plan: stage.plan, result });
  log(`Stage ${stage.order} reported a pushed commit.`);
}

return { stages, completed };
