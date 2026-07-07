-- IAM migration layer 000009 rollback
DROP TRIGGER IF EXISTS trg_auto_seed_workspace_on_user_active ON users;
DROP FUNCTION IF EXISTS auto_seed_workspace_on_user_active();
