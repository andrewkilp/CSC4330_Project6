import type { Call, Response, Turn } from "@switchboard/api";

export type PreviewPhase = "connecting" | "approval" | "paused" | "ended";
export const previewCall: Call = {
  id: "00000000-0000-4000-8000-000000000012",
  owner_id: "00000000-0000-4000-8000-000000000001",
  mode: "mock",
  destination: "+12255550110",
  business_name: "Marigold",
  goal: {
    kind: "restaurant",
    preferred_at: "2026-11-20T19:00:00-06:00",
    timezone: "America/Chicago",
    party_size: 4,
    constraints: ["Ask for patio seating"],
    contact_name: "Fictional Guest",
  },
  status: "connected",
  approval_mode: "always",
  context_revision: 0,
  created_at: "2026-11-13T18:00:00Z",
  updated_at: "2026-11-13T18:00:00Z",
};
export const previewTurns: Turn[] = [
  {
    id: "00000000-0000-4000-8000-000000000101",
    call_id: previewCall.id,
    speaker: "assistant",
    text: "Hi, this is Switchboard, an AI assistant calling on behalf of a guest. I'd like to reserve a table for four this Friday around 7 pm.",
    is_final: true,
    started_at_ms: 0,
    ended_at_ms: 12000,
  },
  {
    id: "00000000-0000-4000-8000-000000000102",
    call_id: previewCall.id,
    speaker: "business",
    text: "Let me check our availability. We're fully booked at 7 pm.",
    is_final: true,
    started_at_ms: 27000,
    ended_at_ms: 34000,
  },
  {
    id: "00000000-0000-4000-8000-000000000103",
    call_id: previewCall.id,
    speaker: "assistant",
    text: "Do you have anything close to 7 pm for a party of four?",
    is_final: true,
    started_at_ms: 63000,
    ended_at_ms: 70000,
  },
  {
    id: "00000000-0000-4000-8000-000000000104",
    call_id: previewCall.id,
    speaker: "business",
    text: "Would 7:30 work instead?",
    is_final: true,
    started_at_ms: 92000,
    ended_at_ms: 96000,
  },
];
export const previewReply: Response = {
  id: "00000000-0000-4000-8000-000000000201",
  call_id: previewCall.id,
  text: "7:30 works. Is patio seating available?",
  context_revision: 0,
  status: "suggested",
  needs_approval: true,
  reason: "Confirm the changed time before responding.",
};
