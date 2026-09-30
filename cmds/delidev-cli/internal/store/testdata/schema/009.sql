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
);
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
PRAGMA user_version=9;
