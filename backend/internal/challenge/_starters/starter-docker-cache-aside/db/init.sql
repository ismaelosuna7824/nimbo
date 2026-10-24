-- The catalog: 200 products.
CREATE TABLE products (
  id          int PRIMARY KEY,
  name        text NOT NULL,
  price_cents int  NOT NULL,
  stock       int  NOT NULL
);
INSERT INTO products
SELECT g, 'Product ' || g, 500 + (g * 37) % 9500, (g * 13) % 120
FROM generate_series(1, 200) AS g;

-- product_page stands in for the joins a real product page runs (reviews,
-- stock across warehouses, pricing rules): about 40 ms of database time
-- per call, the same on every machine.
CREATE FUNCTION product_page(p_id int)
RETURNS TABLE (id int, name text, price_cents int, stock int)
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_sleep(0.04);
  RETURN QUERY SELECT p.id, p.name, p.price_cents, p.stock FROM products p WHERE p.id = p_id;
END
$$;
