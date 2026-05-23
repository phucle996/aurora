"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import Input from "../form/input/InputField";
import Label from "../form/Label";
import Checkbox from "../form/input/Checkbox";
import { EyeCloseIcon, EyeIcon } from "@/icons";
import { authAPI } from "@/lib/api/auth";
import { useRouter } from "next/navigation";

type FormData = {
  fullname: string;
  username: string;
  email: string;
  password: string;
  rePassword: string;
};

type FormErrors = Partial<Record<keyof FormData | "agree", string>>;

function validatePasswordStrength(password: string): string | undefined {
  if (password.length < 8) return "Password must be at least 8 characters.";
  if (!/[a-z]/.test(password)) return "Password must include at least one lowercase letter.";
  if (!/[A-Z]/.test(password)) return "Password must include at least one uppercase letter.";
  if (!/[0-9]/.test(password)) return "Password must include at least one number.";
  if (!/[^A-Za-z0-9]/.test(password)) return "Password must include at least one special character.";
  return undefined;
}

function getPasswordRules(password: string) {
  return {
    minLength: password.length >= 8,
    lowercase: /[a-z]/.test(password),
    uppercase: /[A-Z]/.test(password),
    number: /[0-9]/.test(password),
    special: /[^A-Za-z0-9]/.test(password),
  };
}

export default function SignUpForm() {
  const router = useRouter();
  const [showPassword, setShowPassword] = useState(false);
  const [showRePassword, setShowRePassword] = useState(false);
  const [agree, setAgree] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState("");
  const [submitSuccess, setSubmitSuccess] = useState("");
  const [form, setForm] = useState<FormData>({
    fullname: "",
    username: "",
    email: "",
    password: "",
    rePassword: "",
  });
  const [errors, setErrors] = useState<FormErrors>({});
  const passwordRules = getPasswordRules(form.password);

  const canSubmit = useMemo(() => {
    return (
      agree &&
      form.fullname.trim().length > 0 &&
      form.username.trim().length >= 6 &&
      /.+@.+\..+/.test(form.email.trim()) &&
      form.password.length >= 8 &&
      form.rePassword.length >= 8 &&
      form.password === form.rePassword
    );
  }, [agree, form]);

  function setField<K extends keyof FormData>(field: K, value: string) {
    setForm((prev) => {
      const next = { ...prev, [field]: value };
      const nextErrors: FormErrors = { ...errors };

      if (field === "fullname") {
        nextErrors.fullname = next.fullname.trim() ? undefined : "Full name is required.";
      }
      if (field === "username") {
        nextErrors.username = next.username.trim().length >= 6 ? undefined : "Username must be at least 6 characters.";
      }
      if (field === "email") {
        nextErrors.email = /.+@.+\..+/.test(next.email.trim()) ? undefined : "Email is invalid.";
      }
      if (field === "password") {
        nextErrors.password = validatePasswordStrength(next.password);
        if (next.rePassword.length > 0) {
          nextErrors.rePassword = next.password === next.rePassword ? undefined : "Confirm password does not match.";
        }
      }
      if (field === "rePassword") {
        nextErrors.rePassword = next.password === next.rePassword ? undefined : "Confirm password does not match.";
      }

      setErrors(nextErrors);
      return next;
    });
  }

  function validate(): boolean {
    const next: FormErrors = {};
    if (!form.fullname.trim()) next.fullname = "Full name is required.";
    if (form.username.trim().length < 6) next.username = "Username must be at least 6 characters.";
    if (!/.+@.+\..+/.test(form.email.trim())) next.email = "Email is invalid.";
    const pwdStrengthErr = validatePasswordStrength(form.password);
    if (pwdStrengthErr) next.password = pwdStrengthErr;
    if (form.rePassword.length < 8) next.rePassword = "Confirm password must be at least 8 characters.";
    if (form.password && form.rePassword && form.password !== form.rePassword) {
      next.rePassword = "Confirm password does not match.";
    }
    if (!agree) next.agree = "You must accept terms before creating account.";
    setErrors(next);
    return Object.keys(next).length === 0;
  }

  async function onSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitError("");
    setSubmitSuccess("");
    if (!validate()) return;

    setSubmitting(true);
    try {
      await authAPI.register({
        fullname: form.fullname.trim(),
        username: form.username.trim().toLowerCase(),
        email: form.email.trim().toLowerCase(),
        password: form.password,
        re_password: form.rePassword,
      });
      setSubmitSuccess("Account created. Please sign in.");
      setTimeout(() => {
        router.push("/signin");
      }, 900);
    } catch (error) {
      const message =
        typeof error === "object" &&
        error !== null &&
        "message" in error &&
        typeof (error as { message?: unknown }).message === "string"
          ? (error as { message: string }).message
          : "Unable to register account.";
      setSubmitError(message);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex flex-col flex-1 w-full overflow-y-auto lg:w-1/2 no-scrollbar">
      <div className="w-full max-w-md mx-auto mb-5 sm:pt-10" />

      <div className="flex flex-col justify-center flex-1 w-full max-w-md mx-auto">
        <div>
          <h1 className="mb-2 font-semibold text-gray-800 text-title-sm dark:text-white/90 sm:text-title-md">
            Create account
          </h1>
          <p className="text-sm text-gray-500 dark:text-gray-400">Register with IAM contract-backed fields.</p>

          <form className="mt-6 space-y-5" onSubmit={onSubmit}>
            <div>
              <Label htmlFor="fullname">Full Name<span className="text-error-500">*</span></Label>
              <Input id="fullname" name="fullname" placeholder="Nguyen Van A" onChange={(e) => setField("fullname", e.target.value)} error={Boolean(errors.fullname)} hint={errors.fullname} disabled={submitting} />
            </div>

            <div>
              <Label htmlFor="username">Username<span className="text-error-500">*</span></Label>
              <Input id="username" name="username" placeholder="at least 6 characters" onChange={(e) => setField("username", e.target.value)} error={Boolean(errors.username)} hint={errors.username} disabled={submitting} />
            </div>

            <div>
              <Label htmlFor="email">Email<span className="text-error-500">*</span></Label>
              <Input type="email" id="email" name="email" placeholder="name@domain.com" onChange={(e) => setField("email", e.target.value)} error={Boolean(errors.email)} hint={errors.email} disabled={submitting} />
            </div>

            <div>
              <Label htmlFor="password">Password<span className="text-error-500">*</span></Label>
              <div className="relative">
                <Input id="password" name="password" type={showPassword ? "text" : "password"} placeholder="At least 8 characters" onChange={(e) => setField("password", e.target.value)} hint={errors.password} disabled={submitting} />
                <span onClick={() => setShowPassword(!showPassword)} className="absolute z-30 -translate-y-1/2 cursor-pointer right-4 top-1/2">
                  {showPassword ? <EyeIcon className="fill-gray-500 dark:fill-gray-400" /> : <EyeCloseIcon className="fill-gray-500 dark:fill-gray-400" />}
                </span>
              </div>
              <ul className="mt-2 space-y-1 text-xs">
                <li className={passwordRules.minLength ? "text-success-600" : "text-gray-500"}>- At least 8 characters</li>
                <li className={passwordRules.lowercase ? "text-success-600" : "text-gray-500"}>- One lowercase letter</li>
                <li className={passwordRules.uppercase ? "text-success-600" : "text-gray-500"}>- One uppercase letter</li>
                <li className={passwordRules.number ? "text-success-600" : "text-gray-500"}>- One number</li>
                <li className={passwordRules.special ? "text-success-600" : "text-gray-500"}>- One special character</li>
              </ul>
            </div>

            <div>
              <Label htmlFor="re-password">Confirm Password<span className="text-error-500">*</span></Label>
              <div className="relative">
                <Input id="re-password" name="re-password" type={showRePassword ? "text" : "password"} placeholder="Repeat your password" onChange={(e) => setField("rePassword", e.target.value)} error={Boolean(errors.rePassword)} hint={errors.rePassword} disabled={submitting} />
                <span onClick={() => setShowRePassword(!showRePassword)} className="absolute z-30 -translate-y-1/2 cursor-pointer right-4 top-1/2">
                  {showRePassword ? <EyeIcon className="fill-gray-500 dark:fill-gray-400" /> : <EyeCloseIcon className="fill-gray-500 dark:fill-gray-400" />}
                </span>
              </div>
            </div>

            <div className="flex items-start gap-3">
              <Checkbox className="w-5 h-5" checked={agree} onChange={setAgree} />
              <p className="text-sm text-gray-500 dark:text-gray-400">
                I agree to Terms and Privacy Policy.
              </p>
            </div>
            {errors.agree ? <p className="-mt-3 text-xs text-error-500">{errors.agree}</p> : null}

            {submitError ? <div className="rounded-lg border border-error-200 bg-error-50 px-4 py-3 text-sm text-error-700">{submitError}</div> : null}
            {submitSuccess ? <div className="rounded-lg border border-success-200 bg-success-50 px-4 py-3 text-sm text-success-700">{submitSuccess}</div> : null}

            <button type="submit" disabled={!canSubmit || submitting} className="flex items-center justify-center w-full px-4 py-3 text-sm font-medium text-white transition rounded-lg bg-brand-500 shadow-theme-xs hover:bg-brand-600 disabled:cursor-not-allowed disabled:opacity-60">
              {submitting ? "Creating account..." : "Sign Up"}
            </button>
          </form>

          <div className="mt-5">
            <p className="text-sm font-normal text-center text-gray-700 dark:text-gray-400 sm:text-start">
              Already have an account?{" "}
              <Link href="/signin" className="text-brand-500 hover:text-brand-600 dark:text-brand-400">
                Sign In
              </Link>
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
