-- SPDX-License-Identifier: Apache-2.0
-- Frozen historical DeliDev schema from main 12b33a2accaf. Do not regenerate for new migrations.
PRAGMA application_id=1145848918;
CREATE TABLE entities (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision>0),
 session_id TEXT NOT NULL DEFAULT '', project_id TEXT NOT NULL DEFAULT '', body BLOB NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE INDEX entity_kind_page ON entities(kind,id);
CREATE INDEX entity_session_page ON entities(session_id,kind,id);
CREATE INDEX entity_project_page ON entities(project_id,kind,id);
CREATE TABLE events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 entity_id TEXT NOT NULL, kind TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL, action TEXT NOT NULL, created_at INTEGER NOT NULL
);
CREATE INDEX event_session_cursor ON events(session_id,sequence);
CREATE TABLE receipts(id TEXT PRIMARY KEY,digest TEXT NOT NULL,result BLOB NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE receipt_entities(request_id TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,entity_id TEXT NOT NULL,PRIMARY KEY(request_id,entity_id));
CREATE INDEX receipt_entity ON receipt_entities(entity_id);
CREATE TABLE tombstones(id TEXT PRIMARY KEY,kind TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE credential_verifiers (
 digest BLOB PRIMARY KEY CHECK(length(digest)=32),
 device_id TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE
);
CREATE TABLE pairing_verifiers (
 pairing_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 digest BLOB NOT NULL UNIQUE CHECK(length(digest)=32)
);
CREATE TABLE worker_instances (
 machine_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 instance_id TEXT NOT NULL, last_seen INTEGER NOT NULL
, available_since INTEGER NOT NULL DEFAULT 0);
CREATE TABLE jobs (
 id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 machine_id TEXT NOT NULL, parent_id TEXT NOT NULL, state TEXT NOT NULL,
 accepted_at INTEGER NOT NULL
);
CREATE INDEX jobs_dispatch ON jobs(machine_id,state,accepted_at,id);
CREATE INDEX jobs_parent ON jobs(parent_id,accepted_at,id);
CREATE UNIQUE INDEX model_canonical ON entities(json_extract(body,'$.provider_id'),json_extract(body,'$.native_id')) WHERE kind='model';
CREATE UNIQUE INDEX model_alias ON entities(json_extract(body,'$.alias')) WHERE kind='model' AND COALESCE(json_extract(body,'$.alias'),'')<>'';
CREATE TABLE model_suppressions(provider_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,native_id TEXT NOT NULL,PRIMARY KEY(provider_id,native_id));
CREATE INDEX model_native ON entities(json_extract(body,'$.native_id')) WHERE kind='model';
CREATE INDEX model_display ON entities(json_extract(body,'$.provider_id'),COALESCE(json_extract(body,'$.order'),0),lower(json_extract(body,'$.name')),id) WHERE kind='model';
CREATE INDEX event_kind_cursor ON events(kind,sequence);
CREATE INDEX session_visibility ON entities(project_id,json_extract(body,'$.archive'),id) WHERE kind='session';
CREATE UNIQUE INDEX queue_sequence ON entities(session_id,json_extract(body,'$.sequence')) WHERE kind='queue';
CREATE INDEX queue_pending ON entities(session_id,json_extract(body,'$.delivery'),json_extract(body,'$.sequence')) WHERE kind='queue';
CREATE TABLE job_cancellations (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 requested_at INTEGER NOT NULL
);
CREATE TABLE job_assignments (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL, session_id TEXT NOT NULL, project_id TEXT NOT NULL,
 body BLOB NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE execution_grants (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 digest BLOB NOT NULL UNIQUE CHECK(length(digest)=32),
 execution_id TEXT NOT NULL, machine_id TEXT NOT NULL,
 instance_id TEXT NOT NULL, device_id TEXT NOT NULL, server_epoch TEXT NOT NULL
);
CREATE TABLE execution_references (
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 account_id TEXT NOT NULL, connection_id TEXT NOT NULL, model_id TEXT NOT NULL,
 reference_kind TEXT NOT NULL, native_id TEXT NOT NULL,
 PRIMARY KEY(session_id,account_id,connection_id,model_id,reference_kind,native_id)
);
CREATE TABLE execution_messages (
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 execution_id TEXT NOT NULL,
 native_thread_id TEXT NOT NULL, native_turn_id TEXT NOT NULL, native_item_id TEXT NOT NULL,
 message_id TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE,
 state TEXT NOT NULL CHECK(state IN ('streaming','complete')),
 PRIMARY KEY(session_id,native_thread_id,native_turn_id,native_item_id)
);
CREATE INDEX execution_message_count ON execution_messages(execution_id,state);
CREATE TABLE execution_interactions (
 interaction_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 execution_id TEXT NOT NULL, native_thread_id TEXT NOT NULL, native_request_key TEXT NOT NULL,
 closure TEXT NOT NULL CHECK(closure IN ('open','native-closed','turn-ended')),
 question_bytes INTEGER NOT NULL CHECK(question_bytes > 0 AND question_bytes <= 524288),
 UNIQUE(execution_id,native_request_key)
);
CREATE INDEX execution_interaction_state ON execution_interactions(execution_id,closure);
CREATE UNIQUE INDEX inbox_source ON entities(json_extract(body,'$.source'),json_extract(body,'$.source_id')) WHERE kind='inbox';
CREATE INDEX inbox_read ON entities(json_extract(body,'$.read_state'),id) WHERE kind='inbox';
CREATE INDEX inbox_session_read ON entities(session_id,json_extract(body,'$.read_state'),id) WHERE kind='inbox';
CREATE INDEX schedule_due ON entities(json_extract(body,'$.definition.enabled'),json_extract(body,'$.next_run_at'),id) WHERE kind='schedule';
CREATE UNIQUE INDEX occurrence_position ON entities(json_extract(body,'$.schedule_id'),json_extract(body,'$.sequence')) WHERE kind='occurrence';
CREATE UNIQUE INDEX occurrence_cron_due ON entities(json_extract(body,'$.schedule_id'),json_extract(body,'$.due_at')) WHERE kind='occurrence' AND json_extract(body,'$.trigger')='cron';
CREATE INDEX occurrence_pending ON entities(json_extract(body,'$.state'),json_extract(body,'$.schedule_id'),json_extract(body,'$.sequence'),id) WHERE kind='occurrence';
CREATE INDEX session_schedule ON entities(json_extract(body,'$.schedule_origin.schedule_id'),id) WHERE kind='session';
CREATE TABLE deleted_project_policies (
 project_id TEXT PRIMARY KEY REFERENCES tombstones(id),
 revision INTEGER NOT NULL CHECK(revision>0),
 body BLOB NOT NULL CHECK(length(body)<=131072)
);
CREATE TABLE transcript_search (
 id INTEGER PRIMARY KEY,
 message_id TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 text TEXT NOT NULL
);
CREATE VIRTUAL TABLE transcript_fts USING fts5(text,content='transcript_search',content_rowid='id',tokenize='trigram case_sensitive 1');
CREATE TRIGGER transcript_search_insert AFTER INSERT ON transcript_search BEGIN
 INSERT INTO transcript_fts(rowid,text) VALUES(new.id,new.text);
END;
CREATE TRIGGER transcript_search_delete AFTER DELETE ON transcript_search BEGIN
 INSERT INTO transcript_fts(transcript_fts,rowid,text) VALUES('delete',old.id,old.text);
END;
CREATE TRIGGER transcript_search_update AFTER UPDATE ON transcript_search BEGIN
 INSERT INTO transcript_fts(transcript_fts,rowid,text) VALUES('delete',old.id,old.text);
 INSERT INTO transcript_fts(rowid,text) VALUES(new.id,new.text);
END;
CREATE INDEX transcript_search_session ON transcript_search(session_id);
CREATE INDEX search_execution_job ON entities(json_extract(body,'$.input.execution_id'),session_id)
 WHERE kind='job' AND json_extract(body,'$.type')='execute-session';
CREATE INDEX search_epoch ON events(kind,sequence);
CREATE TABLE response_usage (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 project_id TEXT NOT NULL,
 execution_id TEXT NOT NULL,
 account_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 model_id TEXT NOT NULL,
 response_digest TEXT NOT NULL CHECK(length(response_digest)=64),
 body BLOB NOT NULL CHECK(length(body)<=16384),
 created_at INTEGER NOT NULL, purpose TEXT NOT NULL DEFAULT 'conversation' CHECK(purpose IN ('conversation','session-title')),
 UNIQUE(account_id,provider_id,response_digest)
);
CREATE INDEX response_usage_time ON response_usage(created_at,id);
CREATE INDEX response_usage_session ON response_usage(session_id,created_at,id);
CREATE INDEX response_usage_project ON response_usage(project_id,created_at,id);
CREATE TABLE pricing_versions (
 id TEXT PRIMARY KEY,
 model_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 body BLOB NOT NULL CHECK(length(body)<=16384),
 created_at INTEGER NOT NULL,
 UNIQUE(model_id,revision)
);
CREATE TABLE active_pricing (
 model_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 pricing_id TEXT NOT NULL UNIQUE REFERENCES pricing_versions(id)
);
CREATE TABLE response_estimates (
 usage_id TEXT PRIMARY KEY REFERENCES response_usage(id) ON DELETE CASCADE,
 pricing_id TEXT REFERENCES pricing_versions(id),
 body BLOB NOT NULL CHECK(length(body)<=16384)
);
CREATE TABLE session_estimate_totals (
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 currency TEXT NOT NULL,
 known_amount TEXT NOT NULL CHECK(length(known_amount)<=144),
 complete_responses INTEGER NOT NULL CHECK(complete_responses>=0),
 partial_responses INTEGER NOT NULL CHECK(partial_responses>=0),
 unavailable_responses INTEGER NOT NULL CHECK(unavailable_responses>=0),
 PRIMARY KEY(session_id,currency)
);
CREATE TABLE notification_preferences (
 client_id TEXT PRIMARY KEY,
 revision INTEGER NOT NULL CHECK(revision>1),
 interactions INTEGER NOT NULL CHECK(interactions IN (0,1)),
 terminals INTEGER NOT NULL CHECK(terminals IN (0,1))
);
CREATE TABLE notification_deliveries (
 client_id TEXT NOT NULL,
 inbox_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 claim_id TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK(kind IN ('request','succeeded','failed','stopped')),
 state TEXT NOT NULL CHECK(state IN ('claimed','submitted','denied','failed','uncertain')),
 PRIMARY KEY(client_id,inbox_id)
);
CREATE TABLE pr_problem_sets (
 id TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE,
 stable_key TEXT PRIMARY KEY CHECK(length(stable_key)=64)
);
CREATE TABLE pr_problem_ci_observations (
 id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 set_id TEXT NOT NULL REFERENCES pr_problem_sets(id),
 digest TEXT NOT NULL CHECK(length(digest)=64),
 UNIQUE(set_id,digest)
);
CREATE TABLE pr_problem_records (
 id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 set_id TEXT NOT NULL REFERENCES pr_problem_sets(id),
 kind TEXT NOT NULL CHECK(kind IN ('review-feedback','ci-failure','merge-conflict')),
 native_node TEXT NOT NULL,
 content_version TEXT NOT NULL CHECK(length(content_version)=64),
 current INTEGER NOT NULL CHECK(current IN (0,1)),
 ci_observation_id TEXT REFERENCES pr_problem_ci_observations(id),
 CHECK((kind='ci-failure' AND ci_observation_id IS NOT NULL) OR (kind!='ci-failure' AND ci_observation_id IS NULL)),
 UNIQUE(set_id,kind,native_node,content_version)
);
CREATE INDEX pr_problem_current ON pr_problem_records(set_id,kind,current,id);
CREATE INDEX pr_problem_history ON pr_problem_records(set_id,id);
CREATE TABLE pr_remediation_attempts (
 id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 set_id TEXT NOT NULL REFERENCES pr_problem_sets(id),
 chain_id TEXT NOT NULL,
 sequence INTEGER NOT NULL CHECK(sequence BETWEEN 1 AND 10000),
 state TEXT NOT NULL CHECK(state IN ('reserved','bound','running','uncertain','finished','canceled')),
 input_id TEXT,
 UNIQUE(set_id,sequence),
 UNIQUE(input_id)
);
CREATE UNIQUE INDEX pr_remediation_active ON pr_remediation_attempts(set_id)
 WHERE state IN ('reserved','bound','running','uncertain');
CREATE INDEX pr_remediation_history ON pr_remediation_attempts(set_id,sequence);
CREATE UNIQUE INDEX provider_preset_unique ON entities(json_extract(body,'$.preset_id'))
 WHERE kind='provider' AND json_type(body,'$.preset_id')='text' AND json_extract(body,'$.preset_id')<>'';
CREATE TABLE backup_deletions (
 backup_id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL UNIQUE REFERENCES entities(id),
 request_id TEXT NOT NULL UNIQUE
);
CREATE INDEX response_usage_purpose_time ON response_usage(purpose,created_at,id);
CREATE TABLE session_title_send_claims (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 claimed_at INTEGER NOT NULL
);
CREATE TABLE session_title_http_claims (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 claimed_at INTEGER NOT NULL
);


INSERT INTO metadata(key,value) VALUES('native_accounting_layout','grok-closed-input-v1');
CREATE TABLE native_accounting (
 id TEXT PRIMARY KEY,
 kind INTEGER NOT NULL,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 project_id TEXT NOT NULL,
 execution_id TEXT NOT NULL,
 input_id TEXT NOT NULL,
 account_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 model_id TEXT NOT NULL,
 body BLOB NOT NULL CHECK(length(body)<=16384),
 created_at INTEGER NOT NULL,
 UNIQUE(kind,execution_id,input_id)
);
CREATE INDEX native_accounting_time ON native_accounting(created_at,id);
CREATE INDEX native_accounting_session ON native_accounting(session_id,created_at,id);
CREATE INDEX native_accounting_project ON native_accounting(project_id,created_at,id);



UPDATE metadata SET value='priced-native-input-v2' WHERE key='native_accounting_layout';
ALTER TABLE native_accounting RENAME TO native_accounting_25;
DROP INDEX native_accounting_time;
DROP INDEX native_accounting_session;
DROP INDEX native_accounting_project;
CREATE TABLE native_accounting (
 id TEXT PRIMARY KEY,
 kind INTEGER NOT NULL,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 project_id TEXT NOT NULL,
 execution_id TEXT NOT NULL,
 input_id TEXT NOT NULL,
 account_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 model_id TEXT NOT NULL,
 body BLOB NOT NULL CHECK(length(body)<=16384),
 created_at INTEGER NOT NULL,
 source_id TEXT REFERENCES entities(id) ON DELETE CASCADE,
 pricing_id TEXT REFERENCES pricing_versions(id),
 estimate BLOB CHECK(length(estimate)<=16384)
);
INSERT INTO native_accounting(id,kind,session_id,project_id,execution_id,input_id,account_id,provider_id,model_id,body,created_at)
SELECT id,kind,session_id,project_id,execution_id,input_id,account_id,provider_id,model_id,body,created_at FROM native_accounting_25;
DROP TABLE native_accounting_25;
CREATE UNIQUE INDEX native_accounting_grok_input ON native_accounting(kind,execution_id,input_id) WHERE kind=2;
CREATE UNIQUE INDEX native_accounting_source ON native_accounting(source_id) WHERE source_id IS NOT NULL;
CREATE INDEX native_accounting_time ON native_accounting(created_at,id);
CREATE INDEX native_accounting_session ON native_accounting(session_id,created_at,id);
CREATE INDEX native_accounting_project ON native_accounting(project_id,created_at,id);
CREATE TABLE session_native_estimate_totals (
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 currency TEXT NOT NULL,
 known_amount TEXT NOT NULL CHECK(length(known_amount)<=144),
 complete_units INTEGER NOT NULL CHECK(complete_units>=0),
 partial_units INTEGER NOT NULL CHECK(partial_units>=0),
 unavailable_units INTEGER NOT NULL CHECK(unavailable_units>=0),
 PRIMARY KEY(session_id,currency)
);



CREATE TABLE request_diagnostics (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 execution_id TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 body BLOB NOT NULL CHECK(length(body)<=4096)
);
CREATE INDEX request_diagnostics_session ON request_diagnostics(session_id,id);
CREATE INDEX request_diagnostics_execution ON request_diagnostics(session_id,execution_id,id);

PRAGMA user_version=27;
