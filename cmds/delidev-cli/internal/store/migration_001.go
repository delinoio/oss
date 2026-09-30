// SPDX-License-Identifier: Apache-2.0
package store

const schema = `
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
INSERT INTO metadata(key,value) VALUES('event_floor','0');
PRAGMA application_id=1145848918;
PRAGMA user_version=1;
`
