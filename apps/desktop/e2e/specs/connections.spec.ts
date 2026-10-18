import { expect, test } from "../support/fixtures"

test("connects two resources with a connection recipe", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Connections")
  await app.addFromPalette("sqs", "SQS Queue", "aws_sqs_queue")
  await expect(app.node("aws_sqs_queue.queue")).toBeVisible()
  await app.addFromPalette("lambda function", "Lambda Function", "aws_lambda_function")
  await expect(app.node("aws_lambda_function.function")).toBeVisible()
  await expect(app.saved).toBeVisible()

  // Drag from the queue's output dot to the function's input dot.
  const source = app.node("aws_sqs_queue.queue").locator(".react-flow__handle.source")
  const target = app.node("aws_lambda_function.function").locator(".react-flow__handle.target")
  await source.dragTo(target)

  const dialog = page.getByRole("dialog", { name: "How should these connect?" })
  await expect(dialog.getByRole("button", { name: /^Let the function use this queue/ })).toBeVisible()
  await dialog.getByRole("button", { name: /^Trigger the function from the queue/ }).click()

  await expect(dialog).toBeHidden()
  await expect(page.getByText("Connected", { exact: true })).toBeVisible()
  await expect(app.node("aws_lambda_event_source_mapping.queue_to_function")).toBeVisible()
  await expect(
    app.canvas.getByRole("group", {
      name: "Edge from aws_sqs_queue.queue to aws_lambda_event_source_mapping.queue_to_function",
    }),
  ).toBeAttached()

  const saved = await api.getWorkflow(wf.id)
  expect(saved.edges).toContainEqual(
    expect.objectContaining({ source: "aws_sqs_queue.queue", target: "aws_lambda_event_source_mapping.queue_to_function" }),
  )
})
