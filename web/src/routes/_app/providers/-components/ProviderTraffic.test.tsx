import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";

import { client } from "../../../../client/client.gen";
import { ProviderTraffic } from "./ProviderTraffic";

afterEach(() => client.setConfig({ baseUrl: "", fetch: undefined }));

test("shows the API timeout for provider traffic", async () => {
  const message = "The request log took too long to answer; try a shorter range.";
  client.setConfig({
    baseUrl: "http://localhost",
    fetch: async () =>
      Response.json({ error: { type: "api_error", code: "engine_timeout", message } }, { status: 504 }),
  });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ProviderTraffic name="example" />
    </QueryClientProvider>,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(message);
});
