import { ArrowRightFromSquare, Display, Moon, Sun } from "@gravity-ui/icons";
import { Button, Dropdown, Label, useTheme } from "@heroui/react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute, Outlet, redirect, useNavigate, useRouterState } from "@tanstack/react-router";

import { getSessionOptions, getVersionOptions, logoutMutation } from "../client/@tanstack/react-query.gen";
import { Shell } from "./_app/-components/Shell";

export const Route = createFileRoute("/_app")({
  beforeLoad: async ({ context, location }) => {
    try {
      await context.queryClient.ensureQueryData(getSessionOptions());
    } catch {
      throw redirect({ to: "/login", search: { redirect: location.href } });
    }
  },
  component: AppLayout,
});

const THEMES = [
  { id: "light", label: "Light", icon: Sun },
  { id: "dark", label: "Dark", icon: Moon },
  { id: "system", label: "System", icon: Display },
] as const;

function AppLayout() {
  const { theme, setTheme } = useTheme("system");
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const navigate = useNavigate();
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const version = useQuery(getVersionOptions());
  const logout = useMutation({
    ...logoutMutation(),
    onSettled: async () => {
      queryClient.clear();
      await navigate({ to: "/login", search: { redirect: undefined } });
    },
  });
  const ThemeIcon = THEMES.find((t) => t.id === theme)?.icon ?? Display;

  return (
    <Shell.Root>
      <Shell.Nav current={pathname} footer={version.data ? `remora ${version.data.remora}` : undefined} />
      <div className="flex min-w-0 flex-1 flex-col">
        <Shell.Header>
          <Dropdown>
            <Button isIconOnly variant="ghost" aria-label="Theme">
              <ThemeIcon aria-hidden="true" />
            </Button>
            <Dropdown.Popover placement="bottom end">
              <Dropdown.Menu
                aria-label="Theme"
                selectionMode="single"
                selectedKeys={[theme]}
                onAction={(key) => setTheme(String(key))}
              >
                {THEMES.map(({ id, label, icon: Icon }) => (
                  <Dropdown.Item key={id} id={id} textValue={label}>
                    <Icon aria-hidden="true" className="size-4" />
                    <Label>{label}</Label>
                    <Dropdown.ItemIndicator />
                  </Dropdown.Item>
                ))}
              </Dropdown.Menu>
            </Dropdown.Popover>
          </Dropdown>
          <Button variant="ghost" isPending={logout.isPending} onPress={() => logout.mutate({})}>
            <ArrowRightFromSquare aria-hidden="true" />
            Sign out
          </Button>
        </Shell.Header>
        <Shell.Content>
          <Outlet />
        </Shell.Content>
      </div>
    </Shell.Root>
  );
}
