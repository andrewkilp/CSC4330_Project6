package repository

// ResponseCheckpoint reads published response/turn ordering under the call lock.
// The service decides whether the checkpoint still permits approval or speech.
func (t *Tx) ResponseCheckpoint() (string, bool, error) {
	if err := t.check(); err != nil {
		return "", false, err
	}
	var id string
	var responseSeq, businessSeq int64
	err := t.db.QueryRow(t.ctx, `SELECT COALESCE((SELECT payload->>'id' FROM call_events WHERE call_id=$1 AND type='response.updated' AND context_revision=$2 ORDER BY seq DESC LIMIT 1),''),
 COALESCE((SELECT max(seq) FROM call_events WHERE call_id=$1 AND type='response.updated' AND context_revision=$2),0),
	 COALESCE((SELECT max(seq) FROM call_events WHERE call_id=$1 AND type='turn.updated' AND payload->>'speaker'='business'),0)`, t.id, t.call.ContextRevision).Scan(&id, &responseSeq, &businessSeq)
	return id, businessSeq > responseSeq, safe(err)
}
