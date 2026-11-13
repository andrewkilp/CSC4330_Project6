import {
  type Call,
  createEventState,
  hydrateSnapshot,
  type Response,
  reduceEvent,
  type Turn,
} from "@switchboard/api";

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
    id: "00000000-0000-4000-8000-000000000107",
    call_id: previewCall.id,
    speaker: "business",
    text: "Thanks for calling Marigold. Are you looking for indoor seating or the patio?",
    is_final: true,
    started_at_ms: 14000,
    ended_at_ms: 17000,
  },
  {
    id: "00000000-0000-4000-8000-000000000108",
    call_id: previewCall.id,
    speaker: "assistant",
    text: "The guest prefers the patio, if it is available. Please let me know the options before I confirm anything.",
    is_final: true,
    started_at_ms: 18000,
    ended_at_ms: 26000,
  },
  {
    id: "00000000-0000-4000-8000-000000000109",
    call_id: previewCall.id,
    speaker: "business",
    text: "We may have a later table. Let me look at the patio schedule as well.",
    is_final: true,
    started_at_ms: 40000,
    ended_at_ms: 47000,
  },
  {
    id: "00000000-0000-4000-8000-000000000110",
    call_id: previewCall.id,
    speaker: "assistant",
    text: "Thank you. The requested time is around seven, and I can check a nearby time with the guest.",
    is_final: true,
    started_at_ms: 48000,
    ended_at_ms: 59000,
  },
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

export const replayLabels = [
  "Initial transcript",
  "Partial business turn",
  "Final business turn",
  "New suggestion",
  "Approved, not played",
  "Playback in progress",
  "Playback acknowledged",
];
export function replayTranscript(step: number) {
  let state = hydrateSnapshot(createEventState(previewCall.id), {
    call: previewCall,
    last_seq: 0,
    turns: previewTurns,
    instructions: [],
    responses: [],
  });
  const pending: Turn = {
    id: "00000000-0000-4000-8000-000000000105",
    call_id: previewCall.id,
    speaker: "business",
    text: "The patio",
    is_final: false,
    started_at_ms: 101000,
  };
  const events: {
    type: "turn.updated" | "response.updated";
    payload: Turn | Response;
  }[] = [
    { type: "turn.updated", payload: pending },
    {
      type: "turn.updated",
      payload: {
        ...pending,
        text: "The patio is available. Would you like me to hold 7:30?",
        is_final: true,
        ended_at_ms: 106000,
      },
    },
    { type: "response.updated", payload: previewReply },
    {
      type: "response.updated",
      payload: { ...previewReply, status: "approved" },
    },
    {
      type: "response.updated",
      payload: { ...previewReply, status: "speaking" },
    },
    {
      type: "response.updated",
      payload: { ...previewReply, status: "spoken" },
    },
  ];
  for (let index = 0; index < Math.min(step, events.length); index++) {
    state = reduceEvent(state, {
      ...events[index],
      id: `00000000-0000-4000-8000-${String(index + 300).padStart(12, "0")}`,
      call_id: previewCall.id,
      seq: index + 1,
      occurred_at: "2026-11-13T18:02:00Z",
      context_revision: 0,
    });
  }
  if (step === 6)
    state = reduceEvent(state, {
      id: "00000000-0000-4000-8000-000000000399",
      call_id: previewCall.id,
      seq: 7,
      type: "turn.updated",
      occurred_at: "2026-11-13T18:02:00Z",
      context_revision: 0,
      payload: {
        id: "00000000-0000-4000-8000-000000000106",
        call_id: previewCall.id,
        speaker: "assistant",
        response_id: previewReply.id,
        text: previewReply.text,
        is_final: true,
        started_at_ms: 110000,
        ended_at_ms: 114000,
      },
    });
  return state;
}
