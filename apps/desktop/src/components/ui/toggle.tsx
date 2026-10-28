"use client"

import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"
import { Toggle as TogglePrimitive } from "radix-ui"

const toggleVariants = cva(
  "group/toggle inline-flex items-center justify-center gap-1 rounded-lg text-sm font-medium whitespace-nowrap transition-all duration-150 outline-none hover:bg-muted hover:text-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 aria-pressed:bg-muted data-[state=on]:bg-muted dark:aria-invalid:ring-destructive/40 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: "bg-transparent",
        // Toggle groups use it as a segmented control: the options sit in a muted track, the chosen one is a raised chip.
        // Selected state is read from aria-checked/aria-pressed too: an item wrapped in a TooltipTrigger gets the
        // tooltip's data-state ("closed"), which hides data-state="on". In dark the chip is lighter than the track.
        outline:
          "bg-transparent text-muted-foreground hover:bg-transparent hover:text-foreground data-[size=default]:h-7 data-[size=lg]:h-8 data-[size=sm]:h-6 data-[state=on]:bg-card data-[state=on]:text-foreground data-[state=on]:shadow-sm aria-checked:bg-card aria-checked:text-foreground aria-checked:shadow-sm aria-pressed:bg-card aria-pressed:text-foreground aria-pressed:shadow-sm dark:data-[state=on]:bg-foreground/12 dark:aria-checked:bg-foreground/12 dark:aria-pressed:bg-foreground/12",
      },
      size: {
        default:
          "h-8 min-w-8 px-2.5 has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2",
        sm: "h-7 min-w-7 rounded-[min(var(--radius-md),12px)] px-2.5 text-[0.8rem] has-data-[icon=inline-end]:pr-1.5 has-data-[icon=inline-start]:pl-1.5 [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-9 min-w-9 px-2.5 has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

function Toggle({
  className,
  variant = "default",
  size = "default",
  ...props
}: React.ComponentProps<typeof TogglePrimitive.Root> &
  VariantProps<typeof toggleVariants>) {
  return (
    <TogglePrimitive.Root
      data-slot="toggle"
      className={cn(toggleVariants({ variant, size, className }))}
      {...props}
    />
  )
}

export { Toggle, toggleVariants }
