export const handler = async (event) => {
  const ranAt = new Date().toISOString();
  console.log("scheduled job ran", ranAt, JSON.stringify(event));
  return { ok: true, ranAt };
};
