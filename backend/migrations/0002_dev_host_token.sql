-- 0002 · DEV ONLY: token ชั่วคราวของเครื่องลูก ระหว่างที่ยังไม่มี mTLS (step-ca, เฟส 3)
-- ลบคอลัมน์นี้เมื่อ agent ใช้ client cert ได้แล้ว
ALTER TABLE hosts ADD COLUMN dev_token_hash bytea;
CREATE UNIQUE INDEX hosts_dev_token ON hosts(dev_token_hash) WHERE dev_token_hash IS NOT NULL;
