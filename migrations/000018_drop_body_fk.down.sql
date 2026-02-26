ALTER TABLE request_bodies
  ADD CONSTRAINT request_bodies_transaction_id_fkey
  FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE;
