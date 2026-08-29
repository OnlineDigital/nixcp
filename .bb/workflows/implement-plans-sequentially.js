export const meta = {
  name: "implement-plans-sequentially",
  description: "Discover every numbered NixCP plan and implement, test, commit, and push each stage strictly in documentation order",
  phases: [
    { title: "Discover", detail: "Inventory and order every numbered implementation plan" },
    { title: "Implement", detail: "Implement stages sequentially, committing and pushing after each one" },
  ],
  outputSchema: {
    type: "object",
    required: ["modules", "completed"],
    properties: {
      modules: { type: "array", items: { type: "string" } },
      completed: {
        type: "array",
        items: {
          type: "object",
          required: ["order", "plan", "commit", "pushed", "summary", "tests"],
          properties: {
            order: { type: "integer" },
            plan: { type: "string" },
            commit: { type: "string" },
            pushed: { type: "boolean" },
            summary: { type: "string" },
            tests: { type: "array", items: { type: "string" } },
          },
        },
      },
    },
  },
};

phase("Discover");
const inventory = await agent(
  `You are the discovery owner for the NixCP repository. Work in the current project workspace.

Read plans/overview.md and inspect every plans/*.md file. Identify every numbered implementation-stage document, excluding overview.md. Return all stages in strict numeric/documentation order. Do not edit files, commit, or push.

A stage entry must include:
- order: its integer numeric prefix
- plan: repository-relative path
- title: document title
- objective: concise implementation objective
- dependencies: earlier numbered stages it relies on

Be exhaustive: the repository currently expects a contiguous numbered sequence.`,
  {
    provider: "pi",
    model: "omniroute/xk/deepseek/deepseek-v4-pro",
    reasoningLevel: "none",
    label: "Discover implementation stages",
    phase: "Discover",
    schema: {
      type: "object",
      required: ["modules"],
      properties: {
        modules: {
          type: "array",
          minItems: 1,
          items: {
            type: "object",
            required: ["order", "plan", "title", "objective", "dependencies"],
            properties: {
              order: { type: "integer", minimum: 1 },
              plan: { type: "string" },
              title: { type: "string" },
              objective: { type: "string" },
              dependencies: { type: "array", items: { type: "integer" } },
            },
          },
        },
      },
    },
  },
);

const expectedPlans = [
  "plans/01-product-scope-and-command-contract.md",
  "plans/02-go-architecture-and-cli-foundation.md",
  "plans/03-state-model-and-yaml-schemas.md",
  "plans/04-installation-and-nixos-module-generation.md",
  "plans/05-transactions-locking-and-rebuilds.md",
  "plans/06-system-services.md",
  "plans/07-php-versions-extensions-and-shells.md",
  "plans/08-sites-nginx-and-databases.md",
  "plans/09-security-permissions-and-validation.md",
  "plans/10-testing-milestones-and-release.md",
];

if (inventory.modules.length !== expectedPlans.length) {
  throw new Error(
    `Discovery returned ${inventory.modules.length} stages; expected ${expectedPlans.length}. Refusing partial implementation.`,
  );
}
for (let index = 0; index < expectedPlans.length; index += 1) {
  const stage = inventory.modules[index];
  if (stage.order !== index + 1 || stage.plan !== expectedPlans[index]) {
    throw new Error(
      `Unexpected stage at position ${index + 1}: ${stage.plan}. Expected ${expectedPlans[index]}.`,
    );
  }
}

phase("Implement");
const completed = [];
for (let index = 0; index < inventory.modules.length; index += 1) {
  const stage = inventory.modules[index];
  log(`Starting stage ${stage.order}: ${stage.plan}`);

  const result = await agent(
    `You own NixCP implementation stage ${stage.order} of ${inventory.modules.length}: ${stage.title}.
Plan document: ${stage.plan}
Objective: ${stage.objective}
Dependencies already implemented: ${JSON.stringify(stage.dependencies)}

Work directly in the current repository and current branch. Earlier stages have been implemented, committed, and pushed by preceding workers in this same strictly sequential workflow. Do not use or create a worktree.

Required procedure:
1. Read plans/overview.md, the complete ${stage.plan}, and any earlier plan documents or implementation files needed to understand the existing architecture.
2. Inspect git status and recent history before editing. The tree must be clean. Never reset, revert, amend, squash, or rewrite prior workers' commits.
3. Implement every requirement in this stage that is applicable to the repository now. Preserve all product constraints and permanent exclusions. Do not replace real behavior with TODOs, placeholders, mocks in production code, or knowingly incomplete stubs.
4. Integrate with all prior stages and add/update focused tests. Keep deterministic human and --json behavior where relevant.
5. Run formatting, static checks, unit/integration tests, and build checks appropriate for all code changed so far. If a host-only NixOS operation cannot safely run, test pure generation/validation paths and clearly record the limitation; do not mutate the host OS.
6. Re-read the stage acceptance criteria and inspect the final diff. Fix any defect you find. Ensure no credentials, generated junk, or unrelated changes are included.
7. Commit all stage changes in exactly one new commit with a clear conventional message containing the stage number. Do not amend an existing commit.
8. Push that commit to origin on the current branch. Verify the pushed remote branch resolves to the same commit as local HEAD. A local-only commit is failure.

Do not stop after analysis or merely report recommendations: perform the implementation, tests, commit, and push. If blocked, do not fabricate success and do not create a partial commit; return status "blocked" with precise blockers. Otherwise return status "completed" and the actual full commit SHA, tests run, and concise summary.`,
    {
      provider: "pi",
      model: "omniroute/xk/deepseek/deepseek-v4-pro",
      reasoningLevel: "none",
      label: `Stage ${stage.order}: ${stage.title}`,
      phase: "Implement",
      schema: {
        type: "object",
        required: ["status", "order", "plan", "commit", "pushed", "summary", "tests", "blockers"],
        properties: {
          status: { enum: ["completed", "blocked"] },
          order: { type: "integer" },
          plan: { type: "string" },
          commit: { type: "string" },
          pushed: { type: "boolean" },
          summary: { type: "string" },
          tests: { type: "array", items: { type: "string" } },
          blockers: { type: "array", items: { type: "string" } },
        },
      },
    },
  );

  if (
    result.status !== "completed" ||
    result.order !== stage.order ||
    result.plan !== stage.plan ||
    result.pushed !== true ||
    result.commit.length < 40
  ) {
    throw new Error(
      `Stage ${stage.order} did not complete and push successfully: ${JSON.stringify(result.blockers)}`,
    );
  }

  completed.push({
    order: result.order,
    plan: result.plan,
    commit: result.commit,
    pushed: result.pushed,
    summary: result.summary,
    tests: result.tests,
  });
  log(`Completed and pushed stage ${stage.order} at ${result.commit}`);
}

return {
  modules: inventory.modules.map((module) => stage.plan),
  completed,
};
