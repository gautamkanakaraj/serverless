-- Create the users table
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT UNIQUE NOT NULL
);

-- Create the functions table
CREATE TABLE IF NOT EXISTS functions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    code_content TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT 'javascript',
    public_url TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Index foreign keys for faster queries
CREATE INDEX IF NOT EXISTS idx_functions_user_id ON functions(user_id);

-- Create the execution logs table
CREATE TABLE IF NOT EXISTS execution_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    function_id UUID REFERENCES functions(id) ON DELETE CASCADE,
    log_output TEXT NOT NULL,
    duration_ms INT,
    status_code INT,
    error_message TEXT,
    timestamp TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Index foreign keys for faster queries
CREATE INDEX IF NOT EXISTS idx_execution_logs_function_id ON execution_logs(function_id);

-- Insert dummy user so we can test deployments immediately
INSERT INTO users (id, email) 
VALUES ('123e4567-e89b-12d3-a456-426614174000', 'test@minilambda.com')
ON CONFLICT (id) DO NOTHING;
