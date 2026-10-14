import { render, screen } from "@testing-library/react";

import { About } from "./About";

test("lists remora's version, each engine's version and the credits", () => {
  render(<About remora="0.1.0" engines={[{ name: "router", version: "v1.2.3" }]} />);
  expect(screen.getByText("0.1.0")).toBeInTheDocument();
  expect(screen.getByText("router")).toBeInTheDocument();
  expect(screen.getByText("v1.2.3")).toBeInTheDocument();
  expect(screen.getByText("Built on Bifrost (Apache-2.0) and CLIProxyAPI (MIT)")).toBeInTheDocument();
});
