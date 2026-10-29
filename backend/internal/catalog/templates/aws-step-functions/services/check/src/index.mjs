export const handler = async (input) => {
  const amount = Number(input?.amount ?? 0);
  return { ...input, amount, approved: amount <= 1000 };
};
