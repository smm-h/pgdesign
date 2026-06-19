CREATE TABLE fd_test (
    a int NOT NULL,
    b int NOT NULL,
    c int NOT NULL,
    d int NOT NULL
);

-- Insert data where A->B and B->C hold.
-- a=1 -> b=10, a=2 -> b=20, a=3 -> b=30
-- b=10 -> c=100, b=20 -> c=200, b=30 -> c=300
-- d is independent (varies freely).
INSERT INTO fd_test (a, b, c, d) VALUES
    (1, 10, 100, 1),
    (1, 10, 100, 2),
    (1, 10, 100, 3),
    (2, 20, 200, 1),
    (2, 20, 200, 4),
    (2, 20, 200, 5),
    (3, 30, 300, 2),
    (3, 30, 300, 6),
    (3, 30, 300, 7),
    (1, 10, 100, 8),
    (2, 20, 200, 9),
    (3, 30, 300, 10);
