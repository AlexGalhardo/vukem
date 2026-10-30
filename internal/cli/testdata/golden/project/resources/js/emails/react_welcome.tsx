import { Body, Button, Container, Heading, Html, Preview, Text } from "@react-email/components";
import { go, t } from "./go";

// Props mirror the Go mailable's fields. The defaults are Go template
// actions: `email export` writes them into the HTML and Go escapes the real
// values when the mail is sent.
type Props = { name: string; url: string };

export default function ReactWelcome({ name = go(".Name"), url = go(".URL") }: Partial<Props>) {
	return (
		<Html lang={go("locale")}>
			<Preview>{t("mail.react_welcome.preview")}</Preview>
			<Body style={{ fontFamily: "sans-serif" }}>
				<Container>
					<Heading>{t("mail.react_welcome.title")}</Heading>
					<Text>Hello {name},</Text>
					<Button href={url}>{t("mail.react_welcome.action")}</Button>
				</Container>
			</Body>
		</Html>
	);
}
