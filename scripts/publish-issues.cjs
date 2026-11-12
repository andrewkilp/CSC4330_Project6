/* Publish only this repository's reviewed manifest. Credentials never enter output. */
const fs = require("node:fs");
const path = require("node:path");
const { execFileSync } = require("node:child_process");
const manifest = require("./issues.json");
const reportPath = path.join(__dirname, "issues-publication.json");
const repo = "andrewkilp/CSC4330_Project6";
if (manifest.repository !== repo) throw Error("Unexpected repository");

async function main() {
  const byKey = new Map(manifest.tasks.map((t) => [t.key, t]));
  if (byKey.size !== 50) throw Error("Expected 50 unique tasks");
  for (const person of manifest.people) for (const stage of manifest.stages) {
    if (manifest.tasks.filter((t) => t.owner === person && t.stage === stage.number).length !== 2) throw Error("Unbalanced owner stage");
  }
  const done = new Set(), active = new Set();
  function visit(task) {
    if (active.has(task.key)) throw Error("Dependency cycle");
    if (done.has(task.key)) return;
    active.add(task.key);
    for (const key of task.needs) {
      const dep = byKey.get(key);
      if (!dep || dep.stage > task.stage) throw Error("Invalid dependency");
      visit(dep);
    }
    active.delete(task.key); done.add(task.key);
  }
  manifest.tasks.forEach(visit);
  if (!process.argv.includes("--publish") && !process.argv.includes("--sync-assignees")) {
    console.log("Validated 50 tasks. Use --publish to create/update GitHub issues, or --sync-assignees after invitations are accepted."); return;
  }
  let token = process.env.GITHUB_TOKEN;
  if (!token) {
    const raw = execFileSync("git", ["credential", "fill"], { input: "protocol=https\nhost=github.com\nusername=andrewkilp\n\n", encoding: "utf8", stdio: ["pipe", "pipe", "pipe"] });
    token = raw.split(/\r?\n/).find((line) => line.startsWith("password="))?.slice(9);
  }
  if (!token) throw Error("Sign in to GitHub or set GITHUB_TOKEN locally");
  const headers = { Authorization: `Bearer ${token}`, Accept: "application/vnd.github+json", "Content-Type": "application/json", "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "switchboard-project6" };
  async function api(endpoint, method = "GET", body) {
    const response = await fetch(`https://api.github.com${endpoint}`, { method, headers, body: body ? JSON.stringify(body) : undefined });
    const data = response.status === 204 ? null : await response.json();
    if (!response.ok) throw Error(`${method} ${endpoint}: HTTP ${response.status}: ${data?.message || "request failed"}`);
    return data;
  }
  async function list(endpoint) {
    const rows = [];
    for (let page = 1; ; page++) {
      const next = await api(`${endpoint}${endpoint.includes("?") ? "&" : "?"}per_page=100&page=${page}`);
      rows.push(...next); if (next.length < 100) return rows;
    }
  }
  const account = await api("/user");
  if (account.login.toLowerCase() !== "andrewkilp") throw Error(`Expected andrewkilp, signed in as ${account.login}`);
  const info = await api(`/repos/${repo}`);
  if (!info.permissions?.push) throw Error("Repository write access is required");
  const assignees = new Set((await list(`/repos/${repo}/assignees`)).map((a) => a.login.toLowerCase()));
  const existing = (await list(`/repos/${repo}/issues?state=all`)).filter((i) => !i.pull_request);
  const report = { repository: repo, published_at: new Date().toISOString(), tasks: [] };
  function save() { fs.writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`); }
  const issues = new Map();
  for (const task of manifest.tasks) {
    const matches = existing.filter((i) => i.body?.includes(`<!-- switchboard-task:${task.key} -->`) || (i.title === `${task.key}: ${task.title}` && i.body?.includes(`<!-- switchboard-task:#${i.number} -->`)));
    if (matches.length > 1) throw Error(`Duplicate marker ${task.key}`);
    if (matches[0]) issues.set(task.key, matches[0]);
  }
  if (!process.argv.includes("--sync-assignees")) {
    const labels = await list(`/repos/${repo}/labels`);
    const names = new Set(labels.map((l) => l.name));
    for (const name of new Set(manifest.tasks.flatMap((t) => t.labels))) {
      if (!names.has(name)) await api(`/repos/${repo}/labels`, "POST", { name, color: name.startsWith("stage:") ? "234D3C" : "94733A", description: name.startsWith("owner:") ? "Planned owner, including pending invitations" : "Switchboard development stage" });
    }
    const milestones = await list(`/repos/${repo}/milestones?state=all`);
    const milestoneMap = new Map(milestones.map((m) => [m.title, m.number]));
    for (const stage of manifest.stages) if (!milestoneMap.has(stage.title)) {
      const result = await api(`/repos/${repo}/milestones`, "POST", { title: stage.title, description: `${stage.days} from kickoff. Two tasks per person; ten tasks total. See docs/development-plan.md.` });
      milestoneMap.set(stage.title, result.number);
    }
    for (const task of manifest.tasks) {
      if (!issues.has(task.key)) {
        const issue = await api(`/repos/${repo}/issues`, "POST", { title: `${task.key}: ${task.title}`, body: task.body, labels: task.labels, milestone: milestoneMap.get(task.milestone), assignees: assignees.has(task.owner.toLowerCase()) ? [task.owner] : [] });
        issues.set(task.key, issue);
        console.log(`Created ${task.key}: #${issue.number}`);
        report.tasks.push({ key: task.key, number: issue.number, url: issue.html_url, owner: task.owner, assignees: issue.assignees.map((a) => a.login) }); save();
      }
    }
    // Resolve dependency links only after every actual GitHub number is known.
    for (const task of manifest.tasks) {
      const issue = issues.get(task.key);
      const body = task.body.replace(/\bT\d{2}\b/g, (key) => issues.has(key) ? `#${issues.get(key).number}` : key).replace(/<!-- switchboard-task:#\d+ -->/, `<!-- switchboard-task:${task.key} -->`);
      const assigned = assignees.has(task.owner.toLowerCase()) ? [task.owner] : [];
      const updated = await api(`/repos/${repo}/issues/${issue.number}`, "PATCH", { body, labels: task.labels, milestone: milestoneMap.get(task.milestone), assignees: assigned });
      issues.set(task.key, updated);
    }
  } else {
    for (const task of manifest.tasks) {
      const issue = issues.get(task.key);
      if (!issue) throw Error(`Missing published issue ${task.key}`);
      if (assignees.has(task.owner.toLowerCase()) && !issue.assignees.some((a) => a.login.toLowerCase() === task.owner.toLowerCase())) {
        issues.set(task.key, await api(`/repos/${repo}/issues/${issue.number}`, "PATCH", { assignees: [task.owner] }));
        console.log(`Assigned #${issue.number} to ${task.owner}`);
      }
    }
  }
  report.tasks = manifest.tasks.map((task) => {
    const issue = issues.get(task.key);
    return { key: task.key, number: issue.number, url: issue.html_url, owner: task.owner, stage: task.stage, assignees: issue.assignees.map((a) => a.login), assignment_pending: !issue.assignees.some((a) => a.login.toLowerCase() === task.owner.toLowerCase()) };
  });
  save();
  const lines = ["# Published GitHub issues", "", `Repository: https://github.com/${repo}/issues`, "", "Run scripts/publish-issues.cjs --sync-assignees after a pending collaborator accepts their invitation.", "", "| Task | Issue | Stage | Planned owner | Assigned |", "| --- | --- | --- | --- | --- |", ...report.tasks.map((t) => `| ${t.key} | [#${t.number}](${t.url}) | ${t.stage} | @${t.owner} | ${t.assignees.join(", ") || "Pending repository access"} |`)];
  fs.writeFileSync(path.join(__dirname, "../docs/github-issues.md"), `${lines.join("\n")}\n`);
  const verify = (await list(`/repos/${repo}/issues?state=all`)).filter((i) => !i.pull_request && /<!-- switchboard-task:T\d{2} -->/.test(i.body || ""));
  if (verify.length !== 50) throw Error(`Expected 50 published tasks, found ${verify.length}`);
  console.log(`Verified ${verify.length} issues; ${report.tasks.filter((t) => t.assignment_pending).length} assignments pending repository access.`);
}
main().catch((error) => { console.error(error.message); process.exitCode = 1; });
