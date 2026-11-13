package conversation

// Playback acknowledgement (#30). Synthesis (SynthesizeApproved) only produces audio;
// these two steps record what the call actually did with it:
//
//   - BeginPlayback after every chunk of the approved response has been queued to the
//     media stream and a playback mark was sent behind it: approved -> speaking.
//   - CompletePlayback when that mark comes back from the provider: speaking -> spoken.
//     Only then may the caller add an assistant transcript turn.
//
// Pause, a new instruction, remote speech and end invalidate the response first, so a
// late mark for cleared or obsolete audio is rejected with ErrStaleGeneration and no
// spoken turn is created. A mark means the provider buffer progressed; it is never a
// business confirmation.

// BeginPlayback marks the current approved response as speaking.
func (l *Loop) BeginPlayback(responseID string) (Reply, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.state.Snapshot()
	if s.Response == nil || l.reply.ID != responseID || l.reply.Status != Approved {
		return Reply{}, ErrStaleGeneration
	}
	next, err := l.state.StartSpeaking(responseID, s.Response.Token)
	if err != nil {
		return Reply{}, err
	}
	l.state = next
	l.speechCancel = nil
	l.reply.Status = Speaking
	l.emitLocked()
	return l.reply, nil
}

// CompletePlayback marks the speaking response as spoken and returns it, so the
// caller can record exactly the text that played.
func (l *Loop) CompletePlayback(responseID string) (Reply, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.state.Snapshot()
	if s.Response == nil || l.reply.ID != responseID || l.reply.Status != Speaking {
		return Reply{}, ErrStaleGeneration
	}
	next, err := l.state.MarkSpoken(responseID, s.Response.Token)
	if err != nil {
		return Reply{}, err
	}
	l.state = next
	l.reply.Status = Spoken
	l.emitLocked()
	return l.reply, nil
}
