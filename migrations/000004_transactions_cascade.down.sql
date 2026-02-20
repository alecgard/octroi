ALTER TABLE transactions
    DROP CONSTRAINT transactions_agent_id_fkey,
    ADD CONSTRAINT transactions_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id);

ALTER TABLE transactions
    DROP CONSTRAINT transactions_tool_id_fkey,
    ADD CONSTRAINT transactions_tool_id_fkey FOREIGN KEY (tool_id) REFERENCES tools(id);
