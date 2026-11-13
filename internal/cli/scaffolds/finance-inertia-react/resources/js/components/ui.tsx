import type { ReactNode,SelectHTMLAttributes } from "react";
import { useId } from "react";
import { buttonVariants } from "./ui/button";
import { Card as ShadcnCard,CardHeader,CardTitle,CardContent } from "./ui/card";
import { Field,FieldLabel,FieldError } from "./ui/field";
import { NativeSelect } from "./ui/native-select";
export const primary=buttonVariants();
export const secondary=buttonVariants({variant:'outline'});
export const danger=buttonVariants({variant:'destructive',size:'sm'});
export const input='block w-full rounded-lg border border-input bg-background px-3 py-2 text-sm focus-visible:outline-ring';
export function Card({title,children}:{title?:string;children:ReactNode}){return <ShadcnCard>{title&&<CardHeader><CardTitle>{title}</CardTitle></CardHeader>}<CardContent>{children}</CardContent></ShadcnCard>}
interface SelectProps extends Omit<SelectHTMLAttributes<HTMLSelectElement>,"size">{label:string;error?:string}
export function Select({label,error,children,id,...rest}:SelectProps){const generated=useId();const fieldId=id??generated;return <Field data-invalid={!!error}><FieldLabel htmlFor={fieldId}>{label}</FieldLabel><NativeSelect {...rest} id={fieldId} aria-invalid={!!error}>{children}</NativeSelect><FieldError>{error}</FieldError></Field>}
