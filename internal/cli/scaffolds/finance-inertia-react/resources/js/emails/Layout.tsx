import { Body, Button, Container, Head, Heading, Hr, Html, Preview, Section, Text } from "@react-email/components";
import type { ReactNode } from "react";

const body = { backgroundColor: "#f4f4f5", fontFamily: "Helvetica, Arial, sans-serif", margin: 0, padding: "24px 0" };
const card = { backgroundColor: "#ffffff", borderRadius: "8px", maxWidth: "480px", padding: "32px" };
const muted = { color: "#71717a", fontSize: "12px" };

// Shell is the frame of every finance email: a short heading, the content, a
// button and a footer. The props of each email are Go template actions.
export function Shell({
  preview,
  title,
  children,
  action,
  href,
}: {
  preview: string;
  title: string;
  children: ReactNode;
  action?: string;
  href?: string;
}) {
  return (
    <Html lang="en">
      <Head />
      <Preview>{preview}</Preview>
      <Body style={body}>
        <Container style={card}>
          <Heading as="h1" style={{ fontSize: "20px", margin: "0 0 16px" }}>
            {title}
          </Heading>
          {children}
          {action && href && (
            <Section style={{ margin: "24px 0" }}>
              <Button
                href={href}
                style={{ backgroundColor: "#4f46e5", borderRadius: "6px", color: "#ffffff", padding: "10px 18px" }}
              >
                {action}
              </Button>
            </Section>
          )}
          <Hr />
          <Text style={muted}>You are receiving this email because of your account.</Text>
        </Container>
      </Body>
    </Html>
  );
}
