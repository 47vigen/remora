import { Alert, Button, Card, Spinner } from "@heroui/react";

/** The import dry run: what applying the file would change. Nothing is stored until Apply. */
export function ImportReview({
  diff,
  isPending,
  error,
  onApply,
  onCancel,
}: {
  diff: string | undefined;
  isPending: boolean;
  error?: string;
  onApply(): void;
  onCancel(): void;
}) {
  return (
    <Card>
      <Card.Header>
        <Card.Title>Review the import</Card.Title>
        <Card.Description>Applying makes the store match the file. Items not in the file are removed.</Card.Description>
      </Card.Header>
      <Card.Content>
        {error !== undefined ? (
          <Alert status="danger" role="alert">
            <Alert.Indicator />
            <Alert.Content>
              <Alert.Title>The file cannot be imported</Alert.Title>
              <Alert.Description>{error}</Alert.Description>
            </Alert.Content>
          </Alert>
        ) : diff === undefined ? (
          <Spinner />
        ) : diff === "" ? (
          <p className="text-muted text-sm">No changes.</p>
        ) : (
          <pre className="bg-surface-secondary border-border max-h-96 overflow-auto rounded-sm border p-3 font-mono text-[13px] leading-5 whitespace-pre-wrap">
            {diff}
          </pre>
        )}
      </Card.Content>
      <Card.Footer className="flex gap-2">
        <Button variant="primary" isPending={isPending} isDisabled={!diff || error !== undefined} onPress={onApply}>
          Apply
        </Button>
        <Button variant="secondary" isDisabled={isPending} onPress={onCancel}>
          Cancel
        </Button>
      </Card.Footer>
    </Card>
  );
}
