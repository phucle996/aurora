DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE length(email) > 255) THEN
        RAISE EXCEPTION
            'cannot narrow users.email while values longer than 255 characters exist';
    END IF;
END
$$;

DROP TABLE IF EXISTS external_identities;
ALTER TABLE users
    ALTER COLUMN email TYPE varchar(255);
ALTER TABLE users
    ALTER COLUMN password_hash SET NOT NULL;
