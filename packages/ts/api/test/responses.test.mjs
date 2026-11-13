import assert from "node:assert/strict";
import { afterEach, test } from "node:test";
import { approvalProblem, approveResponse } from "../src/index.ts";

const original = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = original;
});
const callId = "00000000-0000-4000-8000-000000000010";
const responseId = "00000000-0000-4000-8000-000000000044";
const key = "00000000-0000-4000-8000-000000000042";
const approved = {
  id: responseId,
  call_id: callId,
  text: "7:30 works. Patio, please.",
  context_revision: 1,
  status: "approved",
  needs_approval: true,
};
const json = (body, status) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

test("approval binds to the response id and revision and sends only a real edit", async () => {
  const seen = [];
  globalThis.fetch = async (url, init) => {
    seen.push({ url: String(url), body: JSON.parse(init.body), init });
    return json(approved, 200);
  };
  await approveResponse("http://api.test", callId, responseId, {
    contextRevision: 1,
    idempotencyKey: key,
  });
  const result = await approveResponse("http://api.test", callId, responseId, {
    contextRevision: 1,
    editedText: approved.text,
    idempotencyKey: key,
  });
  assert.equal(
    seen[0].url,
    `http://api.test/v1/calls/${callId}/responses/${responseId}/approve`,
  );
  assert.deepEqual(seen[0].body, { context_revision: 1, idempotency_key: key });
  assert.equal(seen[1].body.edited_text, approved.text);
  assert.equal(seen[0].init.credentials, "include");
  // The caller shows the server's version, not its own copy.
  assert.equal(result.text, approved.text);
  assert.equal(result.status, "approved");
});

test("stale approvals are recoverable with the reviewed message", async () => {
  const approve = () =>
    approveResponse("http://api.test", callId, responseId, {
      contextRevision: 0,
      idempotencyKey: key,
    }).catch((e) => e);
  globalThis.fetch = async () =>
    json(
      {
        error: {
          code: "conflict",
          message:
            "This suggestion is out of date. Review the newest suggestion before approving.",
          field: "context_revision",
        },
      },
      409,
    );
  assert.deepEqual(approvalProblem(await approve()), {
    kind: "stale",
    message:
      "This suggestion is out of date. Review the newest suggestion before approving.",
  });
  globalThis.fetch = async () =>
    json(
      {
        error: {
          code: "conflict",
          message: "This response cannot be approved in its current state.",
          field: "status",
        },
      },
      409,
    );
  assert.equal(approvalProblem(await approve()).kind, "stale");
  globalThis.fetch = async () =>
    json({ error: { code: "rate_limited", message: "limit" } }, 429);
  assert.equal(approvalProblem(await approve()).kind, "blocked");
});

test("an answer for another response is not trusted, and an empty edit is never sent", async () => {
  globalThis.fetch = async () =>
    json({ ...approved, id: "00000000-0000-4000-8000-000000000099" }, 200);
  const wrong = await approveResponse("http://api.test", callId, responseId, {
    contextRevision: 1,
    idempotencyKey: key,
  }).catch((e) => e);
  assert.equal(wrong.code, "internal");
  let called = false;
  globalThis.fetch = async () => {
    called = true;
    return json(approved, 200);
  };
  const empty = await approveResponse("http://api.test", callId, responseId, {
    contextRevision: 1,
    editedText: "",
    idempotencyKey: key,
  }).catch((e) => e);
  assert.equal(called, false);
  assert.equal(approvalProblem(empty).kind, "blocked");
});
