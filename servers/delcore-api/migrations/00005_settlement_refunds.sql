-- SPDX-License-Identifier: Apache-2.0
-- Provider refunds apply to an invoice line that can cover several meters.
-- Null service attribution denotes this tenant-level payment adjustment.
ALTER TABLE settlements ALTER COLUMN service_id DROP NOT NULL;
CREATE TABLE settlement_payment_lines (
 transaction_id text NOT NULL, item_id text NOT NULL, job_id uuid NOT NULL REFERENCES jobs(id),
 amount numeric NOT NULL, billed_minor numeric NOT NULL CHECK(billed_minor>0),
 PRIMARY KEY(transaction_id,item_id)
);
CREATE TABLE settlement_refund_effects (
 adjustment_id text NOT NULL, transaction_id text NOT NULL, item_id text NOT NULL,
 billed_minor numeric NOT NULL CHECK(billed_minor>=0),
 PRIMARY KEY(adjustment_id,item_id),
 FOREIGN KEY(transaction_id,item_id) REFERENCES settlement_payment_lines(transaction_id,item_id)
);
